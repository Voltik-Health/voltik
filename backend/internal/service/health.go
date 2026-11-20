package service

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

// HealthStore is what the health service needs from the database.
type HealthStore interface {
	PatientCard(ctx context.Context, patientID uuid.UUID) (domain.PatientCard, error)

	ListDevices(ctx context.Context, patientID uuid.UUID) ([]domain.Device, error)
	RegisterDevice(ctx context.Context, d domain.Device) (domain.Device, bool, error)
	ReplaceSensor(ctx context.Context, patientID, oldID uuid.UUID, d domain.Device) (domain.Device, error)
	UpdateDeviceStatus(ctx context.Context, patientID, deviceID uuid.UUID, apply func(*domain.Device) error) (domain.Device, error)
	RemoveDevice(ctx context.Context, patientID, deviceID uuid.UUID) error
	SetNightscoutSecret(ctx context.Context, patientID, deviceID uuid.UUID, hash []byte) error
	DeviceByNightscoutSecret(ctx context.Context, hash []byte) (domain.Device, error)

	IngestGlucose(ctx context.Context, patientID uuid.UUID, in []domain.ReadingInput) ([]int, []domain.GlucoseReading, error)
	CreateManualReading(ctx context.Context, patientID uuid.UUID, in domain.ReadingInput) (domain.GlucoseReading, bool, error)
	UpdateManualReading(ctx context.Context, patientID uuid.UUID, id int64, apply func(*domain.GlucoseReading) error) (domain.GlucoseReading, error)
	DeleteManualReading(ctx context.Context, patientID uuid.UUID, id int64) error
	ListReadings(ctx context.Context, patientID uuid.UUID, from, to time.Time, limit int, cursor string) ([]domain.GlucoseReading, string, error)
	LatestReading(ctx context.Context, patientID uuid.UUID) (domain.GlucoseReading, error)
	GlucoseSeries(ctx context.Context, patientID uuid.UUID, from, to time.Time, bucket time.Duration) ([]domain.SeriesPoint, error)
	LatestDeviceReadings(ctx context.Context, deviceID uuid.UUID, limit int) ([]domain.GlucoseReading, error)

	IngestHeartRate(ctx context.Context, patientID uuid.UUID, in []domain.HeartRateInput) ([]int, error)
	ListHeartRate(ctx context.Context, patientID uuid.UUID, from, to time.Time, limit int, cursor string) ([]domain.HeartRateSample, string, error)
	LatestHeartRate(ctx context.Context, patientID uuid.UUID) (domain.HeartRateSample, error)

	RaiseAlert(ctx context.Context, n domain.NewAlert, now time.Time) (domain.Alert, bool, error)

	RegisterPushToken(ctx context.Context, userID uuid.UUID, platform, token string) (domain.PushToken, bool, error)
	UnregisterPushToken(ctx context.Context, userID, tokenID uuid.UUID) error
}

// Waker is told when there is new work for a background worker.
type Waker interface{ Wake() }

// PredictionTrigger is told when a patient has new readings, so the
// prediction service can forecast the next 30 minutes (SRS UC112).
//
// DVT-81 (prediction service) implements it with the Go client of the
// service. Until then NoPredictions does nothing. It must never block the
// ingestion (RNF09): run the call in the background, with a timeout.
type PredictionTrigger interface {
	ReadingsArrived(ctx context.Context, patientID uuid.UUID)
}

// NoPredictions is the PredictionTrigger used until DVT-81 plugs in.
type NoPredictions struct{}

func (NoPredictions) ReadingsArrived(context.Context, uuid.UUID) {}

// Health runs devices, glucose readings and heart rate (SRS UC076–UC088),
// and raises the critical alerts of new readings (UC114).
type Health struct {
	store       HealthStore
	alerts      Waker
	predictions PredictionTrigger
	log         *slog.Logger
	now         func() time.Time
}

func NewHealth(store HealthStore, alerts Waker, predictions PredictionTrigger, log *slog.Logger) *Health {
	if predictions == nil {
		predictions = NoPredictions{}
	}
	return &Health{store: store, alerts: alerts, predictions: predictions, log: log, now: time.Now}
}

// patientOf returns the caller's patient profile.
func patientOf(a domain.Account) (uuid.UUID, error) {
	if a.PatientID == nil {
		return uuid.Nil, domain.ErrPatientProfileNotFound
	}
	return *a.PatientID, nil
}

// --- Devices -------------------------------------------------------------------

func (h *Health) ListDevices(ctx context.Context, a domain.Account) ([]domain.Device, error) {
	pid, err := patientOf(a)
	if err != nil {
		return nil, err
	}
	return h.store.ListDevices(ctx, pid)
}

// RegisterDevice pairs a device (UC076). created is false when the patient
// already had a live device with the same serial.
func (h *Health) RegisterDevice(ctx context.Context, a domain.Account, d domain.Device) (domain.Device, bool, error) {
	pid, err := patientOf(a)
	if err != nil {
		return d, false, err
	}
	trimDevice(&d)
	if err := d.ValidateNew(); err != nil {
		return d, false, err
	}
	d.PatientID = pid
	return h.store.RegisterDevice(ctx, d)
}

// ReplaceSensor replaces a CGM sensor with a new one (UC079).
func (h *Health) ReplaceSensor(ctx context.Context, a domain.Account, oldID uuid.UUID, d domain.Device) (domain.Device, error) {
	pid, err := patientOf(a)
	if err != nil {
		return d, err
	}
	trimDevice(&d)
	if err := d.ValidateNew(); err != nil {
		return d, err
	}
	if d.DeviceType != domain.DeviceCGM {
		return d, domain.Invalid("device_type", "the new device must be a cgm_sensor")
	}
	return h.store.ReplaceSensor(ctx, pid, oldID, d)
}

func trimDevice(d *domain.Device) {
	d.BrandModel = strings.TrimSpace(d.BrandModel)
	if d.SerialOrMAC != nil {
		s := strings.TrimSpace(*d.SerialOrMAC)
		d.SerialOrMAC = &s
	}
}

// DeviceStatus is what the app reports about a device (UC078).
type DeviceStatus struct {
	ConnectionStatus *string
	SetBattery       bool
	BatteryPercent   *int
	LastReadingAt    *time.Time
}

// UpdateDeviceStatus applies the app's report. A sensor that was replaced or
// expired keeps that status; a sensor warming up becomes active with its
// first reading.
func (h *Health) UpdateDeviceStatus(ctx context.Context, a domain.Account, deviceID uuid.UUID, st DeviceStatus) (domain.Device, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.Device{}, err
	}
	if st.BatteryPercent != nil && (*st.BatteryPercent < 0 || *st.BatteryPercent > 100) {
		return domain.Device{}, domain.Invalid("battery_percent", "must be between 0 and 100")
	}
	now := h.now()
	if st.LastReadingAt != nil && st.LastReadingAt.After(now.Add(domain.FutureTolerance)) {
		return domain.Device{}, domain.Invalid("last_reading_at", "must not be in the future")
	}
	return h.store.UpdateDeviceStatus(ctx, pid, deviceID, func(d *domain.Device) error {
		if st.SetBattery {
			d.BatteryPercent = st.BatteryPercent
		}
		final := d.ConnectionStatus == domain.DeviceReplaced || d.ConnectionStatus == domain.DeviceExpired
		if st.ConnectionStatus != nil && !final {
			d.ConnectionStatus = *st.ConnectionStatus
		}
		if st.LastReadingAt != nil {
			at := *st.LastReadingAt
			if at.After(now) {
				at = now
			}
			if d.FirstReadingAt == nil && (d.SensorExpiresAt == nil || !at.After(*d.SensorExpiresAt)) {
				d.FirstReadingAt = &at
			}
			synced := now
			d.LastSyncedAt = &synced
			if d.ConnectionStatus == domain.DeviceWarmingUp {
				d.ConnectionStatus = domain.DeviceActive
			}
		}
		return nil
	})
}

func (h *Health) RemoveDevice(ctx context.Context, a domain.Account, deviceID uuid.UUID) error {
	pid, err := patientOf(a)
	if err != nil {
		return err
	}
	return h.store.RemoveDevice(ctx, pid, deviceID)
}

// --- Nightscout uploads (real sensors read by Juggluco or xDrip+) ---------------

// NightscoutSecretHash is how an upload secret is kept and looked up.
// Uploaders send the SHA-1 of the secret (hex) in the api-secret header,
// and some send the secret itself; both give the same hash.
func NightscoutSecretHash(headerValue string) []byte {
	v := strings.ToLower(strings.TrimSpace(headerValue))
	if !sha1Hex.MatchString(v) {
		sum := sha1.Sum([]byte(strings.TrimSpace(headerValue)))
		v = hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256([]byte(v))
	return sum[:]
}

var sha1Hex = regexp.MustCompile(`^[0-9a-f]{40}$`)

// CreateNightscoutSecret makes a new upload secret for a CGM sensor; the old
// one stops working. The secret is returned only this once.
func (h *Health) CreateNightscoutSecret(ctx context.Context, a domain.Account, deviceID uuid.UUID) (string, time.Time, error) {
	pid, err := patientOf(a)
	if err != nil {
		return "", time.Time{}, err
	}
	raw := make([]byte, 10) // 80 bits, 16 characters to type in the app
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	secret := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))
	if err := h.store.SetNightscoutSecret(ctx, pid, deviceID, NightscoutSecretHash(secret)); err != nil {
		return "", time.Time{}, err
	}
	return secret, h.now(), nil
}

// NightscoutDevice finds the sensor of an api-secret header.
func (h *Health) NightscoutDevice(ctx context.Context, apiSecret string) (domain.Device, error) {
	if strings.TrimSpace(apiSecret) == "" {
		return domain.Device{}, domain.ErrUnauthenticated
	}
	d, err := h.store.DeviceByNightscoutSecret(ctx, NightscoutSecretHash(apiSecret))
	if errors.Is(err, domain.ErrNotFound) {
		return d, domain.ErrUnauthenticated
	}
	return d, err
}

// NightscoutEntry is an entry of the Nightscout API.
type NightscoutEntry struct {
	Type       string
	SGV        *int
	MBG        *int
	DateMillis int64
	DateString string
	Direction  string
	Device     string
}

// IngestNightscout stores the sgv entries as readings of the sensor and the
// mbg entries as manual readings, and returns the entries it accepted.
func (h *Health) IngestNightscout(ctx context.Context, d domain.Device, entries []NightscoutEntry) ([]NightscoutEntry, error) {
	var in []domain.ReadingInput
	var accepted []NightscoutEntry
	now := h.now()
	for _, e := range entries {
		at := time.UnixMilli(e.DateMillis).UTC()
		var r domain.ReadingInput
		switch {
		case e.Type == "sgv" && e.SGV != nil:
			id := d.ID
			r = domain.ReadingInput{
				IdempotencyKey: fmt.Sprintf("ns:%s:%d", d.ID, e.DateMillis), MeasuredAt: at, ValueMgDl: *e.SGV,
				Trend: domain.TrendFromNightscout(e.Direction), Source: domain.SourceCGM, DeviceID: &id,
			}
		case e.Type == "mbg" && e.MBG != nil:
			r = domain.ReadingInput{
				IdempotencyKey: fmt.Sprintf("ns-mbg:%s:%d", d.ID, e.DateMillis), MeasuredAt: at, ValueMgDl: *e.MBG,
				Trend: domain.TrendUnknown, Source: domain.SourceManual,
			}
		default:
			continue
		}
		if code, _ := r.Check(now); code != "" {
			continue
		}
		in = append(in, r)
		accepted = append(accepted, e)
	}
	if len(in) == 0 {
		return []NightscoutEntry{}, nil
	}
	if _, err := h.ingest(ctx, d.PatientID, in); err != nil {
		return nil, err
	}
	return accepted, nil
}

// NightscoutEntries returns the sensor's latest readings as Nightscout entries.
func (h *Health) NightscoutEntries(ctx context.Context, d domain.Device, count int) ([]NightscoutEntry, error) {
	readings, err := h.store.LatestDeviceReadings(ctx, d.ID, count)
	if err != nil {
		return nil, err
	}
	out := make([]NightscoutEntry, 0, len(readings))
	for _, r := range readings {
		v := r.ValueMgDl
		out = append(out, NightscoutEntry{
			Type: "sgv", SGV: &v, DateMillis: r.MeasuredAt.UnixMilli(),
			DateString: r.MeasuredAt.UTC().Format("2006-01-02T15:04:05.000Z"),
			Direction:  domain.NightscoutDirection(r.Trend), Device: "voltik",
		})
	}
	return out, nil
}

// --- Glucose readings ----------------------------------------------------------

// IngestGlucose stores a batch from the app (UC081): invalid readings are
// rejected one by one, the others stored once.
func (h *Health) IngestGlucose(ctx context.Context, a domain.Account, in []domain.ReadingInput) (domain.BatchResult, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.BatchResult{}, err
	}
	now := h.now()
	res := domain.BatchResult{Rejected: []domain.RejectedItem{}}
	var valid []domain.ReadingInput
	var index []int
	for i, r := range in {
		if code, msg := r.Check(now); code != "" {
			res.Rejected = append(res.Rejected, rejected(i, r.IdempotencyKey, code, msg))
			continue
		}
		valid, index = append(valid, r), append(index, i)
	}
	outcomes, err := h.ingest(ctx, pid, valid)
	if err != nil {
		return res, err
	}
	for k, o := range outcomes {
		switch o {
		case domain.OutcomeInserted:
			res.Inserted++
		case domain.OutcomeDuplicate:
			res.Duplicates++
		case domain.OutcomeUnknownDevice:
			res.Rejected = append(res.Rejected, rejected(index[k], valid[k].IdempotencyKey, domain.RejectUnknownDevice,
				"device_id is not one of the patient's devices"))
		}
	}
	sort.Slice(res.Rejected, func(i, j int) bool { return res.Rejected[i].Index < res.Rejected[j].Index })
	return res, nil
}

func rejected(i int, key, code, msg string) domain.RejectedItem {
	var k *string
	if key != "" {
		k = &key
	}
	return domain.RejectedItem{Index: i, IdempotencyKey: k, Code: code, Message: msg}
}

// ingest stores checked readings and reacts to the new ones.
func (h *Health) ingest(ctx context.Context, patientID uuid.UUID, in []domain.ReadingInput) ([]int, error) {
	if len(in) == 0 {
		return nil, nil
	}
	outcomes, inserted, err := h.store.IngestGlucose(ctx, patientID, in)
	if err != nil {
		return nil, err
	}
	h.afterReadings(ctx, patientID, inserted)
	return outcomes, nil
}

// afterReadings raises the critical alerts of new readings that are less
// than 2 hours old (UC114, UC081 A3) and asks for a new prediction. A
// failure here never undoes the readings (RNF09).
func (h *Health) afterReadings(ctx context.Context, patientID uuid.UUID, inserted []domain.GlucoseReading) {
	if len(inserted) == 0 {
		return
	}
	card, err := h.store.PatientCard(ctx, patientID)
	if err != nil {
		h.log.Error("alerts: could not read the patient's limits", "error", err)
		return
	}
	sort.Slice(inserted, func(i, j int) bool { return inserted[i].MeasuredAt.Before(inserted[j].MeasuredAt) })
	now := h.now()
	raised := false
	for _, r := range inserted {
		if now.Sub(r.MeasuredAt) > domain.AlertMaxAge {
			continue
		}
		n, ok := domain.CriticalAlertFor(r, card.Thresholds)
		if !ok {
			continue
		}
		if _, created, err := h.store.RaiseAlert(ctx, n, now); err != nil {
			h.log.Error("alerts: could not raise a critical alert", "error", err)
		} else if created {
			raised = true
		}
	}
	if raised && h.alerts != nil {
		h.alerts.Wake()
	}
	h.predictions.ReadingsArrived(ctx, patientID)
}

// CreateManualReading stores a finger-prick reading (UC083).
func (h *Health) CreateManualReading(ctx context.Context, a domain.Account, in domain.ReadingInput) (domain.GlucoseReading, bool, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.GlucoseReading{}, false, err
	}
	in.Source, in.DeviceID, in.Trend = domain.SourceManual, nil, domain.TrendUnknown
	if code, msg := in.Check(h.now()); code != "" {
		field := "value_mg_dl"
		switch {
		case strings.HasPrefix(msg, "idempotency_key"):
			field = "idempotency_key"
		case strings.HasPrefix(msg, "measured_at"):
			field = "measured_at"
		}
		return domain.GlucoseReading{}, false, domain.Invalid(field, msg)
	}
	r, created, err := h.store.CreateManualReading(ctx, pid, in)
	if err == nil && created {
		h.afterReadings(ctx, pid, []domain.GlucoseReading{r})
	}
	return r, created, err
}

// UpdateManualReading corrects a manual reading (UC084).
func (h *Health) UpdateManualReading(ctx context.Context, a domain.Account, id int64, value *int, measuredAt *time.Time) (domain.GlucoseReading, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.GlucoseReading{}, err
	}
	now := h.now()
	var v domain.ValidationError
	if value != nil && (*value < domain.MinGlucoseMgDl || *value > domain.MaxGlucoseMgDl) {
		v.Add("value_mg_dl", "must be between 20 and 550")
	}
	if measuredAt != nil && (measuredAt.After(now.Add(domain.FutureTolerance)) || measuredAt.Before(domain.EarliestMeasurement)) {
		v.Add("measured_at", "must be after 2020-01-01 and not in the future")
	}
	if err := v.OrNil(); err != nil {
		return domain.GlucoseReading{}, err
	}
	return h.store.UpdateManualReading(ctx, pid, id, func(r *domain.GlucoseReading) error {
		if value != nil {
			r.ValueMgDl = *value
		}
		if measuredAt != nil {
			r.MeasuredAt = *measuredAt
		}
		return nil
	})
}

func (h *Health) DeleteManualReading(ctx context.Context, a domain.Account, id int64) error {
	pid, err := patientOf(a)
	if err != nil {
		return err
	}
	return h.store.DeleteManualReading(ctx, pid, id)
}

func (h *Health) ListReadings(ctx context.Context, a domain.Account, from, to time.Time, limit int, cursor string) ([]domain.GlucoseReading, string, error) {
	pid, err := patientOf(a)
	if err != nil {
		return nil, "", err
	}
	if err := domain.CheckRange(from, to); err != nil {
		return nil, "", err
	}
	return h.store.ListReadings(ctx, pid, from, to, limit, cursor)
}

func (h *Health) LatestReading(ctx context.Context, a domain.Account) (domain.GlucoseReading, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.GlucoseReading{}, err
	}
	return h.store.LatestReading(ctx, pid)
}

// Series groups the readings in buckets of 15 minutes or an hour (UC086).
func (h *Health) Series(ctx context.Context, a domain.Account, from, to time.Time, resolution string) ([]domain.SeriesPoint, error) {
	pid, err := patientOf(a)
	if err != nil {
		return nil, err
	}
	if err := domain.CheckRange(from, to); err != nil {
		return nil, err
	}
	bucket := 15 * time.Minute
	if resolution == "1h" {
		bucket = time.Hour
	}
	return h.store.GlucoseSeries(ctx, pid, from, to, bucket)
}

// --- Heart rate ------------------------------------------------------------------

// IngestHeartRate stores a batch of heart rate samples (UC087).
func (h *Health) IngestHeartRate(ctx context.Context, a domain.Account, in []domain.HeartRateInput) (domain.BatchResult, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.BatchResult{}, err
	}
	now := h.now()
	res := domain.BatchResult{Rejected: []domain.RejectedItem{}}
	var valid []domain.HeartRateInput
	var index []int
	for i, s := range in {
		if code, msg := s.Check(now); code != "" {
			res.Rejected = append(res.Rejected, rejected(i, s.IdempotencyKey, code, msg))
			continue
		}
		valid, index = append(valid, s), append(index, i)
	}
	if len(valid) > 0 {
		outcomes, err := h.store.IngestHeartRate(ctx, pid, valid)
		if err != nil {
			return res, err
		}
		for k, o := range outcomes {
			switch o {
			case domain.OutcomeInserted:
				res.Inserted++
			case domain.OutcomeDuplicate:
				res.Duplicates++
			default:
				res.Rejected = append(res.Rejected, rejected(index[k], valid[k].IdempotencyKey, domain.RejectUnknownDevice,
					"device_id is not one of the patient's devices"))
			}
		}
	}
	sort.Slice(res.Rejected, func(i, j int) bool { return res.Rejected[i].Index < res.Rejected[j].Index })
	return res, nil
}

func (h *Health) ListHeartRate(ctx context.Context, a domain.Account, from, to time.Time, limit int, cursor string) ([]domain.HeartRateSample, string, error) {
	pid, err := patientOf(a)
	if err != nil {
		return nil, "", err
	}
	if err := domain.CheckRange(from, to); err != nil {
		return nil, "", err
	}
	return h.store.ListHeartRate(ctx, pid, from, to, limit, cursor)
}

func (h *Health) LatestHeartRate(ctx context.Context, a domain.Account) (domain.HeartRateSample, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.HeartRateSample{}, err
	}
	return h.store.LatestHeartRate(ctx, pid)
}

// --- Push tokens (UC008) ---------------------------------------------------------

func (h *Health) RegisterPushToken(ctx context.Context, a domain.Account, platform, token string) (domain.PushToken, bool, error) {
	token = strings.TrimSpace(token)
	var v domain.ValidationError
	switch platform {
	case "android", "ios", "web":
	default:
		v.Add("platform", "must be android, ios or web")
	}
	if token == "" || utf8.RuneCountInString(token) > 500 {
		v.Add("push_token", "must have between 1 and 500 characters")
	}
	if err := v.OrNil(); err != nil {
		return domain.PushToken{}, false, err
	}
	return h.store.RegisterPushToken(ctx, a.ID, platform, token)
}

func (h *Health) UnregisterPushToken(ctx context.Context, a domain.Account, id uuid.UUID) error {
	return h.store.UnregisterPushToken(ctx, a.ID, id)
}

// IngestSimulated stores readings of the staging simulator like any other
// source, raising the alerts of the live ones.
func (h *Health) IngestSimulated(ctx context.Context, patientID uuid.UUID, in []domain.ReadingInput) error {
	_, err := h.ingest(ctx, patientID, in)
	return err
}
