package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/voltik/voltik/backend/internal/database"
	"github.com/voltik/voltik/backend/internal/domain"
)

const readingColumns = `id, patient_id, device_id, measured_at, value_mg_dl, trend, source, signal_quality, idempotency_key, created_at`

func scanReading(row pgx.Row) (domain.GlucoseReading, error) {
	var r domain.GlucoseReading
	err := row.Scan(&r.ID, &r.PatientID, &r.DeviceID, &r.MeasuredAt, &r.ValueMgDl, &r.Trend, &r.Source,
		&r.SignalQuality, &r.IdempotencyKey, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.GlucoseReading{}, domain.ErrNotFound
	}
	return r, err
}

func collectReadings(rows pgx.Rows, err error) ([]domain.GlucoseReading, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.GlucoseReading, error) { return scanReading(row) })
}

// IngestGlucose stores readings that the service already checked, for one
// patient, in one transaction (UC081). For each input it says whether it was
// inserted, was already stored (same idempotency_key), or names a device the
// patient does not have. It also updates the devices that sent data and
// resolves their "sensor without data" alerts (UC092 A1).
func (s *Store) IngestGlucose(ctx context.Context, patientID uuid.UUID, in []domain.ReadingInput) ([]int, []domain.GlucoseReading, error) {
	outcomes := make([]int, len(in))
	var inserted []domain.GlucoseReading
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		known, err := patientDevices(ctx, tx, patientID, deviceIDs(in, func(r domain.ReadingInput) *uuid.UUID { return r.DeviceID }))
		if err != nil {
			return err
		}
		var devices, keys, trends, sources []string
		var times []time.Time
		var values, qualities []int
		for i, r := range in {
			device := ""
			if r.DeviceID != nil {
				if !known[*r.DeviceID] {
					outcomes[i] = domain.OutcomeUnknownDevice
					continue
				}
				device = r.DeviceID.String()
			}
			trend := r.Trend
			if trend == "" {
				trend = domain.TrendUnknown
			}
			quality := -1
			if r.SignalQuality != nil {
				quality = *r.SignalQuality
			}
			outcomes[i] = domain.OutcomeDuplicate // until the insert says otherwise
			devices, keys, trends, sources = append(devices, device), append(keys, r.IdempotencyKey), append(trends, trend), append(sources, r.Source)
			times, values, qualities = append(times, r.MeasuredAt), append(values, r.ValueMgDl), append(qualities, quality)
		}
		if len(keys) == 0 {
			return nil
		}
		inserted, err = collectReadings(tx.Query(ctx, `
			INSERT INTO glucose_readings (patient_id, device_id, measured_at, value_mg_dl, trend, source, signal_quality, idempotency_key)
			SELECT $1, NULLIF(u.device_id, '')::uuid, u.measured_at, u.value, u.trend, u.source, NULLIF(u.quality, -1), u.key
			  FROM unnest($2::text[], $3::timestamptz[], $4::int[], $5::text[], $6::text[], $7::int[], $8::text[])
			       AS u(device_id, measured_at, value, trend, source, quality, key)
			ON CONFLICT (idempotency_key) DO NOTHING
			RETURNING `+readingColumns,
			patientID, devices, times, values, trends, sources, qualities, keys))
		if err != nil {
			return err
		}
		markInserted(in, outcomes, inserted)
		return touchDevices(ctx, tx, devices, times, true)
	})
	return outcomes, inserted, err
}

// markInserted turns the outcome of the first input with each inserted key into domain.OutcomeInserted.
func markInserted(in []domain.ReadingInput, outcomes []int, inserted []domain.GlucoseReading) {
	keys := map[string]bool{}
	for _, r := range inserted {
		keys[*r.IdempotencyKey] = true
	}
	for i, r := range in {
		if outcomes[i] == domain.OutcomeDuplicate && keys[r.IdempotencyKey] {
			outcomes[i] = domain.OutcomeInserted
			delete(keys, r.IdempotencyKey)
		}
	}
}

func deviceIDs[T any](items []T, id func(T) *uuid.UUID) []uuid.UUID {
	var out []uuid.UUID
	for _, it := range items {
		if d := id(it); d != nil {
			out = append(out, *d)
		}
	}
	return out
}

// patientDevices returns which of the ids are live devices of the patient.
func patientDevices(ctx context.Context, tx pgx.Tx, patientID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	known := map[uuid.UUID]bool{}
	if len(ids) == 0 {
		return known, nil
	}
	rows, err := tx.Query(ctx, `SELECT id FROM devices WHERE patient_id = $1 AND deleted_at IS NULL AND id = ANY($2)`, patientID, ids)
	if err != nil {
		return nil, err
	}
	found, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	for _, id := range found {
		known[id] = true
	}
	return known, err
}

// touchDevices records that data arrived from these devices: the sync time,
// the first reading and, for a sensor, that it is active again.
func touchDevices(ctx context.Context, tx pgx.Tx, devices []string, times []time.Time, sensor bool) error {
	rows, err := tx.Query(ctx, `
		UPDATE devices d SET
		       last_synced_at = GREATEST(now(), x.max_at),
		       first_reading_at = CASE
		           WHEN d.first_reading_at IS NOT NULL THEN LEAST(d.first_reading_at, x.min_at)
		           WHEN d.sensor_expires_at IS NULL OR x.min_at <= d.sensor_expires_at THEN x.min_at
		       END,
		       connection_status = CASE WHEN d.connection_status IN ('warming_up', 'disconnected')
		                                THEN 'active' ELSE d.connection_status END
		  FROM (SELECT u.device_id::uuid AS id, min(u.measured_at) AS min_at, max(u.measured_at) AS max_at
		          FROM unnest($1::text[], $2::timestamptz[]) AS u(device_id, measured_at)
		         WHERE u.device_id <> '' GROUP BY 1) x
		 WHERE d.id = x.id
		RETURNING d.id`, devices, times)
	if err != nil {
		return err
	}
	touched, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil || !sensor || len(touched) == 0 {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE alerts SET ack_status = 'resolved', acknowledged_at = GREATEST(now(), triggered_at),
		       resolution_notes = 'O sensor voltou a enviar leituras.', next_escalation_at = NULL
		 WHERE device_id = ANY($1) AND alert_type = 'sensor_disconnected' AND ack_status = 'pending'`, touched)
	return err
}

// CreateManualReading stores a finger-prick reading typed by the patient
// (UC083). created is false when the idempotency_key was already stored.
func (s *Store) CreateManualReading(ctx context.Context, patientID uuid.UUID, in domain.ReadingInput) (domain.GlucoseReading, bool, error) {
	r, err := scanReading(s.pool.QueryRow(ctx, `
		INSERT INTO glucose_readings (patient_id, measured_at, value_mg_dl, trend, source, idempotency_key)
		VALUES ($1, $2, $3, 'unknown', 'manual_fingerstick', $4)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING `+readingColumns, patientID, in.MeasuredAt, in.ValueMgDl, in.IdempotencyKey))
	if err == nil {
		return r, true, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return r, false, err
	}
	r, err = scanReading(s.pool.QueryRow(ctx, `SELECT `+readingColumns+` FROM glucose_readings
		 WHERE idempotency_key = $1 AND patient_id = $2 AND deleted_at IS NULL`, in.IdempotencyKey, patientID))
	if errors.Is(err, domain.ErrNotFound) {
		return r, false, domain.Invalid("idempotency_key", "is already used")
	}
	return r, false, err
}

// UpdateManualReading lets apply correct a manual reading (UC084). Readings
// of sensors and platforms answer ErrReadingImmutable.
func (s *Store) UpdateManualReading(ctx context.Context, patientID uuid.UUID, id int64, apply func(*domain.GlucoseReading) error) (domain.GlucoseReading, error) {
	var r domain.GlucoseReading
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		r, err = scanReading(tx.QueryRow(ctx, `SELECT `+readingColumns+` FROM glucose_readings
			 WHERE id = $1 AND patient_id = $2 AND deleted_at IS NULL FOR UPDATE`, id, patientID))
		if err != nil {
			return err
		}
		if r.Source != domain.SourceManual {
			return domain.ErrReadingImmutable
		}
		if err := apply(&r); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE glucose_readings SET value_mg_dl = $2, measured_at = $3 WHERE id = $1`,
			r.ID, r.ValueMgDl, r.MeasuredAt)
		return err
	})
	return r, err
}

// DeleteManualReading deletes a manual reading logically (UC084).
func (s *Store) DeleteManualReading(ctx context.Context, patientID uuid.UUID, id int64) error {
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		r, err := scanReading(tx.QueryRow(ctx, `SELECT `+readingColumns+` FROM glucose_readings
			 WHERE id = $1 AND patient_id = $2 AND deleted_at IS NULL FOR UPDATE`, id, patientID))
		if err != nil {
			return err
		}
		if r.Source != domain.SourceManual {
			return domain.ErrReadingImmutable
		}
		_, err = tx.Exec(ctx, `UPDATE glucose_readings SET deleted_at = now() WHERE id = $1`, id)
		return err
	})
}

// ListReadings returns one page of readings in [from, to), oldest first.
func (s *Store) ListReadings(ctx context.Context, patientID uuid.UUID, from, to time.Time, limit int, cursor string) ([]domain.GlucoseReading, string, error) {
	var afterAt *time.Time
	var afterID int64
	if cursor != "" {
		t, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		if afterID, err = strconv.ParseInt(id, 10, 64); err != nil {
			return nil, "", domain.Invalid("cursor", "is not a cursor returned by the API")
		}
		afterAt = &t
	}
	items, err := collectReadings(s.pool.Query(ctx, `SELECT `+readingColumns+` FROM glucose_readings
		 WHERE patient_id = $1 AND deleted_at IS NULL AND measured_at >= $2 AND measured_at < $3
		   AND ($4::timestamptz IS NULL OR (measured_at, id) > ($4, $5))
		 ORDER BY measured_at, id LIMIT $6`, patientID, from, to, afterAt, afterID, limit+1))
	if err != nil || len(items) <= limit {
		return items, "", err
	}
	last := items[limit-1]
	return items[:limit], encodeCursor(last.MeasuredAt, strconv.FormatInt(last.ID, 10)), nil
}

// LatestReading is the patient's most recent reading (ErrNotFound when there is none).
func (s *Store) LatestReading(ctx context.Context, patientID uuid.UUID) (domain.GlucoseReading, error) {
	return scanReading(s.pool.QueryRow(ctx, `SELECT `+readingColumns+` FROM glucose_readings
		 WHERE patient_id = $1 AND deleted_at IS NULL ORDER BY measured_at DESC, id DESC LIMIT 1`, patientID))
}

// GlucoseSeries groups the readings of [from, to) in buckets of the given length.
func (s *Store) GlucoseSeries(ctx context.Context, patientID uuid.UUID, from, to time.Time, bucket time.Duration) ([]domain.SeriesPoint, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT date_bin($4::interval, measured_at, $2) AS bucket,
		       round(avg(value_mg_dl))::int, min(value_mg_dl), max(value_mg_dl), count(*)::int
		  FROM glucose_readings
		 WHERE patient_id = $1 AND deleted_at IS NULL AND measured_at >= $2 AND measured_at < $3
		 GROUP BY 1 ORDER BY 1`, patientID, from, to, fmt.Sprintf("%d seconds", int(bucket.Seconds())))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.SeriesPoint, error) {
		var p domain.SeriesPoint
		err := row.Scan(&p.BucketStart, &p.Mean, &p.Min, &p.Max, &p.Count)
		return p, err
	})
}

// ReadingPoints returns the values of [from, to), oldest first, for statistics.
func (s *Store) ReadingPoints(ctx context.Context, patientID uuid.UUID, from, to time.Time) ([]domain.Point, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT measured_at, value_mg_dl FROM glucose_readings
		 WHERE patient_id = $1 AND deleted_at IS NULL AND measured_at >= $2 AND measured_at < $3
		 ORDER BY measured_at`, patientID, from, to)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Point, error) {
		var p domain.Point
		err := row.Scan(&p.At, &p.Value)
		return p, err
	})
}

// LatestDeviceReadings returns a sensor's latest readings, newest first (Nightscout GET).
func (s *Store) LatestDeviceReadings(ctx context.Context, deviceID uuid.UUID, limit int) ([]domain.GlucoseReading, error) {
	return collectReadings(s.pool.Query(ctx, `SELECT `+readingColumns+` FROM glucose_readings
		 WHERE device_id = $1 AND deleted_at IS NULL ORDER BY measured_at DESC LIMIT $2`, deviceID, limit))
}

// ExistingSlots returns the 5-minute slots of [from, to) where the device
// already has a reading (the simulator fills only the others).
func (s *Store) ExistingSlots(ctx context.Context, deviceID uuid.UUID, from, to time.Time) (map[int64]bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT (extract(epoch FROM measured_at)::bigint / 300)
		  FROM glucose_readings WHERE device_id = $1 AND measured_at >= $2 AND measured_at < $3`, deviceID, from, to)
	if err != nil {
		return nil, err
	}
	slots, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	out := make(map[int64]bool, len(slots))
	for _, sl := range slots {
		out[sl] = true
	}
	return out, err
}

// --- Heart rate ------------------------------------------------------------

const heartRateColumns = `h.id, h.device_id, h.measured_at, h.bpm, h.idempotency_key, h.created_at`

func scanHeartRate(row pgx.Row) (domain.HeartRateSample, error) {
	var h domain.HeartRateSample
	err := row.Scan(&h.ID, &h.DeviceID, &h.MeasuredAt, &h.BPM, &h.IdempotencyKey, &h.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return h, domain.ErrNotFound
	}
	return h, err
}

// IngestHeartRate stores samples already checked by the service (UC087). A
// sample with a stored key, or for a device and instant already stored, is a
// duplicate.
func (s *Store) IngestHeartRate(ctx context.Context, patientID uuid.UUID, in []domain.HeartRateInput) ([]int, error) {
	outcomes := make([]int, len(in))
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		known, err := patientDevices(ctx, tx, patientID, deviceIDs(in, func(h domain.HeartRateInput) *uuid.UUID { return &h.DeviceID }))
		if err != nil {
			return err
		}
		var devices, keys []string
		var times []time.Time
		var bpms []int
		for i, h := range in {
			if !known[h.DeviceID] {
				outcomes[i] = domain.OutcomeUnknownDevice
				continue
			}
			outcomes[i] = domain.OutcomeDuplicate
			devices, keys, times, bpms = append(devices, h.DeviceID.String()), append(keys, h.IdempotencyKey), append(times, h.MeasuredAt), append(bpms, h.BPM)
		}
		if len(keys) == 0 {
			return nil
		}
		rows, err := tx.Query(ctx, `
			INSERT INTO heart_rate_samples (device_id, measured_at, bpm, idempotency_key)
			SELECT u.device_id::uuid, u.measured_at, u.bpm, u.key
			  FROM unnest($1::text[], $2::timestamptz[], $3::int[], $4::text[]) AS u(device_id, measured_at, bpm, key)
			ON CONFLICT DO NOTHING
			RETURNING idempotency_key`, devices, times, bpms, keys)
		if err != nil {
			return err
		}
		stored, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		done := map[string]bool{}
		for _, k := range stored {
			done[k] = true
		}
		for i, h := range in {
			if outcomes[i] == domain.OutcomeDuplicate && done[h.IdempotencyKey] {
				outcomes[i] = domain.OutcomeInserted
				delete(done, h.IdempotencyKey)
			}
		}
		return touchDevices(ctx, tx, devices, times, false)
	})
	return outcomes, err
}

// ListHeartRate returns one page of the samples of the patient's devices in [from, to).
func (s *Store) ListHeartRate(ctx context.Context, patientID uuid.UUID, from, to time.Time, limit int, cursor string) ([]domain.HeartRateSample, string, error) {
	var afterAt *time.Time
	var afterID int64
	if cursor != "" {
		t, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		if afterID, err = strconv.ParseInt(id, 10, 64); err != nil {
			return nil, "", domain.Invalid("cursor", "is not a cursor returned by the API")
		}
		afterAt = &t
	}
	rows, err := s.pool.Query(ctx, `SELECT `+heartRateColumns+`
		  FROM heart_rate_samples h JOIN devices d ON d.id = h.device_id
		 WHERE d.patient_id = $1 AND h.measured_at >= $2 AND h.measured_at < $3
		   AND ($4::timestamptz IS NULL OR (h.measured_at, h.id) > ($4, $5))
		 ORDER BY h.measured_at, h.id LIMIT $6`, patientID, from, to, afterAt, afterID, limit+1)
	if err != nil {
		return nil, "", err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.HeartRateSample, error) { return scanHeartRate(row) })
	if err != nil || len(items) <= limit {
		return items, "", err
	}
	last := items[limit-1]
	return items[:limit], encodeCursor(last.MeasuredAt, strconv.FormatInt(last.ID, 10)), nil
}

// LatestHeartRate is the newest sample of the patient's devices.
func (s *Store) LatestHeartRate(ctx context.Context, patientID uuid.UUID) (domain.HeartRateSample, error) {
	return scanHeartRate(s.pool.QueryRow(ctx, `SELECT `+heartRateColumns+`
		  FROM heart_rate_samples h JOIN devices d ON d.id = h.device_id
		 WHERE d.patient_id = $1 ORDER BY h.measured_at DESC, h.id DESC LIMIT 1`, patientID))
}
