package repository

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/voltik/voltik/backend/internal/database"
	"github.com/voltik/voltik/backend/internal/domain"
)

// --- Insulin (UC109–UC111) -----------------------------------------------------

const insulinColumns = `
	l.id, l.patient_id, it.code, l.insulin_brand, l.dose_units, l.administered_at, l.reason, l.meal_id,
	l.notes, l.idempotency_key, l.created_at, l.updated_at`

const insulinFrom = ` FROM insulin_logs l JOIN insulin_types it ON it.id = l.insulin_type_id`

func scanInsulin(row pgx.Row) (domain.InsulinLog, error) {
	var l domain.InsulinLog
	err := row.Scan(&l.ID, &l.PatientID, &l.InsulinType, &l.Brand, &l.DoseUnits, &l.AdministeredAt, &l.Reason,
		&l.MealID, &l.Notes, &l.IdempotencyKey, &l.CreatedAt, &l.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, domain.ErrNotFound
	}
	return l, err
}

func collectInsulin(rows pgx.Rows, err error) ([]domain.InsulinLog, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.InsulinLog, error) { return scanInsulin(row) })
}

func insulinTypeID(ctx context.Context, q querier, code string) (int16, error) {
	var id int16
	err := q.QueryRow(ctx, `SELECT id FROM insulin_types WHERE code = $1 AND is_active`, code).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.Invalid("insulin_type", "must be one of the listed values")
	}
	return id, err
}

func (s *Store) insulinByID(ctx context.Context, patientID, id uuid.UUID) (domain.InsulinLog, error) {
	return scanInsulin(s.pool.QueryRow(ctx, `SELECT `+insulinColumns+insulinFrom+`
		 WHERE l.id = $1 AND l.patient_id = $2 AND l.deleted_at IS NULL`, id, patientID))
}

// CreateInsulinLog stores a dose (UC109). created is false when the
// idempotency_key was already stored: a resent dose is never stored twice.
func (s *Store) CreateInsulinLog(ctx context.Context, l domain.InsulinLog) (domain.InsulinLog, bool, error) {
	typeID, err := insulinTypeID(ctx, s.pool, l.InsulinType)
	if err != nil {
		return l, false, err
	}
	var id uuid.UUID
	err = s.pool.QueryRow(ctx, `
		INSERT INTO insulin_logs (patient_id, meal_id, insulin_type_id, insulin_brand, dose_units, administered_at,
		                          reason, notes, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (idempotency_key) DO NOTHING RETURNING id`,
		l.PatientID, l.MealID, typeID, l.Brand, l.DoseUnits, l.AdministeredAt, l.Reason, l.Notes, l.IdempotencyKey).Scan(&id)
	switch {
	case database.IsForeignKeyViolation(err):
		return l, false, domain.Invalid("meal_id", "is not one of your meals")
	case errors.Is(err, pgx.ErrNoRows):
		existing, err := scanInsulin(s.pool.QueryRow(ctx, `SELECT `+insulinColumns+insulinFrom+`
			 WHERE l.idempotency_key = $1 AND l.patient_id = $2 AND l.deleted_at IS NULL`, l.IdempotencyKey, l.PatientID))
		if errors.Is(err, domain.ErrNotFound) {
			return l, false, domain.Invalid("idempotency_key", "is already used")
		}
		return existing, false, err
	case err != nil:
		return l, false, err
	}
	created, err := s.insulinByID(ctx, l.PatientID, id)
	return created, true, err
}

// UpdateInsulinLog lets apply correct a dose (UC110).
func (s *Store) UpdateInsulinLog(ctx context.Context, patientID, id uuid.UUID, apply func(*domain.InsulinLog) error) (domain.InsulinLog, error) {
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := scanInsulin(tx.QueryRow(ctx, `SELECT `+insulinColumns+insulinFrom+`
			 WHERE l.id = $1 AND l.patient_id = $2 AND l.deleted_at IS NULL FOR UPDATE OF l`, id, patientID))
		if err != nil {
			return err
		}
		if err := apply(&l); err != nil {
			return err
		}
		typeID, err := insulinTypeID(ctx, tx, l.InsulinType)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE insulin_logs SET insulin_type_id = $2, insulin_brand = $3, dose_units = $4, administered_at = $5,
			       reason = $6, meal_id = $7, notes = $8
			 WHERE id = $1`, id, typeID, l.Brand, l.DoseUnits, l.AdministeredAt, l.Reason, l.MealID, l.Notes)
		if database.IsForeignKeyViolation(err) {
			return domain.Invalid("meal_id", "is not one of your meals")
		}
		return err
	})
	if err != nil {
		return domain.InsulinLog{}, err
	}
	return s.insulinByID(ctx, patientID, id)
}

// DeleteInsulinLog deletes a dose logically.
func (s *Store) DeleteInsulinLog(ctx context.Context, patientID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `UPDATE insulin_logs SET deleted_at = now() WHERE id = $1 AND patient_id = $2 AND deleted_at IS NULL`, id, patientID)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

// ListInsulinLogs returns the doses of [from, to), oldest first, with the
// totals per day (in Portugal) and insulin type (UC111).
func (s *Store) ListInsulinLogs(ctx context.Context, patientID uuid.UUID, from, to time.Time) ([]domain.InsulinLog, []domain.InsulinDailyTotal, error) {
	logs, err := collectInsulin(s.pool.Query(ctx, `SELECT `+insulinColumns+insulinFrom+`
		 WHERE l.patient_id = $1 AND l.deleted_at IS NULL AND l.administered_at >= $2 AND l.administered_at < $3
		 ORDER BY l.administered_at, l.id`, patientID, from, to))
	if err != nil {
		return nil, nil, err
	}
	type key struct {
		day  time.Time
		kind string
	}
	sums := map[key]*domain.InsulinDailyTotal{}
	for _, l := range logs {
		k := key{domain.LisbonDate(l.AdministeredAt), l.InsulinType}
		if sums[k] == nil {
			sums[k] = &domain.InsulinDailyTotal{Date: k.day, InsulinType: k.kind}
		}
		sums[k].TotalUnits = domain.Round2(sums[k].TotalUnits + l.DoseUnits)
		sums[k].Doses++
	}
	totals := make([]domain.InsulinDailyTotal, 0, len(sums))
	for _, t := range sums {
		totals = append(totals, *t)
	}
	sort.Slice(totals, func(i, j int) bool {
		if !totals[i].Date.Equal(totals[j].Date) {
			return totals[i].Date.Before(totals[j].Date)
		}
		return totals[i].InsulinType < totals[j].InsulinType
	})
	return logs, totals, nil
}

// --- Physical activity (UC089–UC091) -------------------------------------------

const activityColumns = `a.id, a.patient_id, a.activity_name, a.started_at, a.duration_minutes, a.intensity, a.total_steps,
	a.avg_heart_rate_bpm, a.calories_burned_kcal, a.device_id, a.idempotency_key, a.created_at, a.updated_at`

func scanActivity(row pgx.Row) (domain.Activity, error) {
	var a domain.Activity
	err := row.Scan(&a.ID, &a.PatientID, &a.Name, &a.StartedAt, &a.DurationMinutes, &a.Intensity, &a.TotalSteps,
		&a.AvgHeartRateBPM, &a.CaloriesKcal, &a.DeviceID, &a.IdempotencyKey, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, domain.ErrNotFound
	}
	return a, err
}

func (s *Store) activityByID(ctx context.Context, patientID, id uuid.UUID) (domain.Activity, error) {
	return scanActivity(s.pool.QueryRow(ctx, `SELECT `+activityColumns+` FROM physical_activity_logs a
		 WHERE a.id = $1 AND a.patient_id = $2 AND a.deleted_at IS NULL`, id, patientID))
}

// CreateActivity stores an activity (UC089). created is false when the
// idempotency_key was already stored.
func (s *Store) CreateActivity(ctx context.Context, a domain.Activity) (domain.Activity, bool, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO physical_activity_logs (patient_id, device_id, activity_name, started_at, duration_minutes, intensity,
		                                    total_steps, avg_heart_rate_bpm, calories_burned_kcal, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (idempotency_key) DO NOTHING RETURNING id`,
		a.PatientID, a.DeviceID, a.Name, a.StartedAt, a.DurationMinutes, a.Intensity, a.TotalSteps,
		a.AvgHeartRateBPM, a.CaloriesKcal, a.IdempotencyKey).Scan(&id)
	switch {
	case database.IsForeignKeyViolation(err):
		return a, false, domain.Invalid("device_id", "is not one of your devices")
	case errors.Is(err, pgx.ErrNoRows):
		existing, err := scanActivity(s.pool.QueryRow(ctx, `SELECT `+activityColumns+` FROM physical_activity_logs a
			 WHERE a.idempotency_key = $1 AND a.patient_id = $2 AND a.deleted_at IS NULL`, a.IdempotencyKey, a.PatientID))
		if errors.Is(err, domain.ErrNotFound) {
			return a, false, domain.Invalid("idempotency_key", "is already used")
		}
		return existing, false, err
	case err != nil:
		return a, false, err
	}
	created, err := s.activityByID(ctx, a.PatientID, id)
	return created, true, err
}

// IngestActivities stores sessions imported from a watch (UC090), with the
// outcome of each item as in IngestGlucose.
func (s *Store) IngestActivities(ctx context.Context, patientID uuid.UUID, in []domain.Activity) ([]int, error) {
	outcomes := make([]int, len(in))
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		known, err := patientDevices(ctx, tx, patientID, deviceIDs(in, func(a domain.Activity) *uuid.UUID { return a.DeviceID }))
		if err != nil {
			return err
		}
		for i, a := range in {
			if a.DeviceID != nil && !known[*a.DeviceID] {
				outcomes[i] = domain.OutcomeUnknownDevice
				continue
			}
			tag, err := tx.Exec(ctx, `
				INSERT INTO physical_activity_logs (patient_id, device_id, activity_name, started_at, duration_minutes,
				                                    intensity, total_steps, avg_heart_rate_bpm, calories_burned_kcal, idempotency_key)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
				ON CONFLICT (idempotency_key) DO NOTHING`,
				patientID, a.DeviceID, a.Name, a.StartedAt, a.DurationMinutes, a.Intensity, a.TotalSteps,
				a.AvgHeartRateBPM, a.CaloriesKcal, a.IdempotencyKey)
			if err != nil {
				return err
			}
			outcomes[i] = domain.OutcomeDuplicate
			if tag.RowsAffected() == 1 {
				outcomes[i] = domain.OutcomeInserted
			}
		}
		return nil
	})
	return outcomes, err
}

// UpdateActivity lets apply correct an activity (UC091).
func (s *Store) UpdateActivity(ctx context.Context, patientID, id uuid.UUID, apply func(*domain.Activity) error) (domain.Activity, error) {
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		a, err := scanActivity(tx.QueryRow(ctx, `SELECT `+activityColumns+` FROM physical_activity_logs a
			 WHERE a.id = $1 AND a.patient_id = $2 AND a.deleted_at IS NULL FOR UPDATE`, id, patientID))
		if err != nil {
			return err
		}
		if err := apply(&a); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE physical_activity_logs SET activity_name = $2, started_at = $3, duration_minutes = $4, intensity = $5,
			       total_steps = $6, avg_heart_rate_bpm = $7, calories_burned_kcal = $8
			 WHERE id = $1`, id, a.Name, a.StartedAt, a.DurationMinutes, a.Intensity, a.TotalSteps, a.AvgHeartRateBPM, a.CaloriesKcal)
		return err
	})
	if err != nil {
		return domain.Activity{}, err
	}
	return s.activityByID(ctx, patientID, id)
}

// DeleteActivity deletes an activity logically.
func (s *Store) DeleteActivity(ctx context.Context, patientID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `UPDATE physical_activity_logs SET deleted_at = now()
		 WHERE id = $1 AND patient_id = $2 AND deleted_at IS NULL`, id, patientID)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

// ListActivities returns the activities that start in [from, to), oldest first.
func (s *Store) ListActivities(ctx context.Context, patientID uuid.UUID, from, to time.Time) ([]domain.Activity, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+activityColumns+` FROM physical_activity_logs a
		 WHERE a.patient_id = $1 AND a.deleted_at IS NULL AND a.started_at >= $2 AND a.started_at < $3
		 ORDER BY a.started_at, a.id`, patientID, from, to)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Activity, error) { return scanActivity(row) })
}

// ActiveMinutes adds up the minutes of the activities that start in [from, to).
func (s *Store) ActiveMinutes(ctx context.Context, patientID uuid.UUID, from, to time.Time) (int, error) {
	var minutes int
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(sum(duration_minutes), 0)::int FROM physical_activity_logs
		 WHERE patient_id = $1 AND deleted_at IS NULL AND started_at >= $2 AND started_at < $3`,
		patientID, from, to).Scan(&minutes)
	return minutes, err
}

// HasActiveConsent reports whether the user accepted, and did not revoke, the document type.
func (s *Store) HasActiveConsent(ctx context.Context, userID uuid.UUID, docType string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM consents c JOIN legal_documents d ON d.id = c.document_id
		                WHERE c.user_id = $1 AND d.document_type = $2 AND c.revoked_at IS NULL)`, userID, docType).Scan(&ok)
	return ok, err
}
