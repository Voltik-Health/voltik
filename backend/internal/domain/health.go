package domain

import (
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // Europe/Lisbon even where the system has no zone files
	"unicode/utf8"

	"github.com/google/uuid"
)

// Lisbon is the time zone of Voltik's days: the Diário, reports and jobs
// count calendar days in Portugal.
var Lisbon = mustLoadLocation("Europe/Lisbon")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// LisbonDate is the calendar day in Portugal of an instant, at 00:00 UTC
// (how DATE columns are scanned).
func LisbonDate(t time.Time) time.Time {
	y, m, d := t.In(Lisbon).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// DayStart is when a calendar day starts in Portugal.
func DayStart(date time.Time) time.Time {
	y, m, d := date.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, Lisbon)
}

// Glucose sources (glucose_readings.source).
const (
	SourceCGM           = "cgm_sensor"
	SourceManual        = "manual_fingerstick"
	SourceHealthKit     = "healthkit_sync"
	SourceHealthConnect = "health_connect_sync"
)

// Trends of a reading.
const (
	TrendRisingFast  = "rising_fast"
	TrendRising      = "rising"
	TrendStable      = "stable"
	TrendFalling     = "falling"
	TrendFallingFast = "falling_fast"
	TrendUnknown     = "unknown"
)

// Device types and connection statuses.
const (
	DeviceCGM        = "cgm_sensor"
	DeviceSmartwatch = "smartwatch"
	DeviceSmartband  = "smartband"
	DeviceGlucometer = "glucometer"

	DeviceActive       = "active"
	DeviceDisconnected = "disconnected"
	DeviceWarmingUp    = "warming_up"
	DeviceExpired      = "expired"
	DeviceReplaced     = "replaced"
)

// SimulatorBrandModel marks the CGM sensors that the simulator feeds in staging.
const SimulatorBrandModel = "Voltik CGM simulator"

// Limits of the health tables (migrations 000001–000003).
const (
	MinGlucoseMgDl  = 20
	MaxGlucoseMgDl  = 550
	MinHeartRateBPM = 25
	MaxHeartRateBPM = 250
	MaxBatchItems   = 500
	MaxRangeDays    = 90

	// A reading may be a little ahead of the server clock (phone clocks drift).
	FutureTolerance = 5 * time.Minute
	// Readings older than this are stored but raise no critical alert (UC081 A3).
	AlertMaxAge = 2 * time.Hour

	maxKeyLength        = 100
	maxBrandModelLength = 100
	maxSerialLength     = 100
)

// EarliestMeasurement is the oldest instant the database accepts.
var EarliestMeasurement = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// GlucoseReading is a row of glucose_readings.
type GlucoseReading struct {
	ID             int64
	PatientID      uuid.UUID
	DeviceID       *uuid.UUID
	MeasuredAt     time.Time
	ValueMgDl      int
	Trend          string
	Source         string
	SignalQuality  *int
	IdempotencyKey *string
	CreatedAt      time.Time
}

// ReadingInput is a reading sent by an app or an uploader.
type ReadingInput struct {
	IdempotencyKey string
	MeasuredAt     time.Time
	ValueMgDl      int
	Trend          string // empty: unknown
	Source         string
	DeviceID       *uuid.UUID
	SignalQuality  *int
}

// Check returns the reason the reading can never be stored, or "" when it is valid.
func (r ReadingInput) Check(now time.Time) (code, message string) {
	switch {
	case strings.TrimSpace(r.IdempotencyKey) == "" || utf8.RuneCountInString(r.IdempotencyKey) > maxKeyLength:
		return RejectValidation, "idempotency_key must have between 1 and 100 characters"
	case r.ValueMgDl < MinGlucoseMgDl || r.ValueMgDl > MaxGlucoseMgDl:
		return RejectValidation, "value_mg_dl must be between 20 and 550"
	case r.MeasuredAt.Before(EarliestMeasurement):
		return RejectValidation, "measured_at must be after 2020-01-01"
	case r.MeasuredAt.After(now.Add(FutureTolerance)):
		return RejectFuture, "measured_at is more than 5 minutes ahead of the server clock"
	case r.Source != SourceCGM && r.Source != SourceManual && r.Source != SourceHealthKit && r.Source != SourceHealthConnect:
		return RejectValidation, "source must be one of the listed values"
	case r.Trend != "" && !validTrend(r.Trend):
		return RejectValidation, "trend must be one of the listed values"
	case r.SignalQuality != nil && (*r.SignalQuality < 0 || *r.SignalQuality > 100):
		return RejectValidation, "signal_quality must be between 0 and 100"
	case r.DeviceID != nil && r.Source != SourceCGM:
		return RejectValidation, "only cgm_sensor readings have a device_id"
	}
	return "", ""
}

func validTrend(t string) bool {
	switch t {
	case TrendRisingFast, TrendRising, TrendStable, TrendFalling, TrendFallingFast, TrendUnknown:
		return true
	}
	return false
}

// HeartRateSample is a row of heart_rate_samples.
type HeartRateSample struct {
	ID             int64
	DeviceID       uuid.UUID
	MeasuredAt     time.Time
	BPM            int
	IdempotencyKey *string
	CreatedAt      time.Time
}

// HeartRateInput is a sample sent by the app.
type HeartRateInput struct {
	IdempotencyKey string
	MeasuredAt     time.Time
	BPM            int
	DeviceID       uuid.UUID
}

func (h HeartRateInput) Check(now time.Time) (code, message string) {
	switch {
	case strings.TrimSpace(h.IdempotencyKey) == "" || utf8.RuneCountInString(h.IdempotencyKey) > maxKeyLength:
		return RejectValidation, "idempotency_key must have between 1 and 100 characters"
	case h.BPM < MinHeartRateBPM || h.BPM > MaxHeartRateBPM:
		return RejectValidation, "bpm must be between 25 and 250"
	case h.MeasuredAt.Before(EarliestMeasurement):
		return RejectValidation, "measured_at must be after 2020-01-01"
	case h.MeasuredAt.After(now.Add(FutureTolerance)):
		return RejectFuture, "measured_at is more than 5 minutes ahead of the server clock"
	}
	return "", ""
}

// Outcome of each item of a batch upload, in the order of the input.
const (
	OutcomeInserted = iota
	OutcomeDuplicate
	OutcomeUnknownDevice
)

// Rejection codes of a batch upload (contract).
const (
	RejectValidation    = "validation_failed"
	RejectUnknownDevice = "unknown_device"
	RejectFuture        = "measured_at_in_future"
)

// BatchResult is the outcome of a batch upload.
type BatchResult struct {
	Inserted   int
	Duplicates int
	Rejected   []RejectedItem
}

// RejectedItem is an item of a batch that will never be accepted.
type RejectedItem struct {
	Index          int
	IdempotencyKey *string
	Code           string
	Message        string
}

// Device is a row of devices.
type Device struct {
	ID               uuid.UUID
	PatientID        uuid.UUID
	DeviceType       string
	BrandModel       string
	SerialOrMAC      *string
	ConnectionStatus string
	BatteryPercent   *int
	FirstReadingAt   *time.Time
	SensorExpiresAt  *time.Time
	LastSyncedAt     *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// InUse reports whether a CGM sensor still counts as the patient's sensor.
func (d Device) InUse() bool {
	switch d.ConnectionStatus {
	case DeviceActive, DeviceWarmingUp, DeviceDisconnected:
		return true
	}
	return false
}

// ValidateNew checks a device the patient registers.
func (d Device) ValidateNew() error {
	var v ValidationError
	switch d.DeviceType {
	case DeviceCGM, DeviceSmartwatch, DeviceSmartband, DeviceGlucometer:
	default:
		v.Add("device_type", "must be one of the listed values")
	}
	checkText(&v, "brand_model", d.BrandModel, maxBrandModelLength)
	if d.SerialOrMAC != nil {
		checkText(&v, "serial_or_mac", *d.SerialOrMAC, maxSerialLength)
	}
	if d.BatteryPercent != nil && (*d.BatteryPercent < 0 || *d.BatteryPercent > 100) {
		v.Add("battery_percent", "must be between 0 and 100")
	}
	if d.SensorExpiresAt != nil && d.DeviceType != DeviceCGM {
		v.Add("sensor_expires_at", "only CGM sensors expire")
	}
	return v.OrNil()
}

// SeriesPoint is one bucket of a long glucose chart.
type SeriesPoint struct {
	BucketStart time.Time
	Mean        int
	Min         int
	Max         int
	Count       int
}

// CheckRange validates a from/to range of at most MaxRangeDays.
func CheckRange(from, to time.Time) error {
	switch {
	case !to.After(from):
		return Invalid("to", "must be after from")
	case to.Sub(from) > MaxRangeDays*24*time.Hour:
		return Invalid("to", fmt.Sprintf("the range can be at most %d days", MaxRangeDays))
	}
	return nil
}

// CheckDateRange validates an inclusive range of calendar days.
func CheckDateRange(fromField, toField string, from, to time.Time) error {
	switch {
	case to.Before(from):
		return Invalid(toField, "must not be before "+fromField)
	case to.Sub(from) >= MaxRangeDays*24*time.Hour:
		return Invalid(toField, fmt.Sprintf("the range can be at most %d days", MaxRangeDays))
	}
	return nil
}

// TrendFromNightscout maps a Nightscout direction to a Voltik trend.
func TrendFromNightscout(direction string) string {
	switch direction {
	case "DoubleUp", "SingleUp":
		return TrendRisingFast
	case "FortyFiveUp":
		return TrendRising
	case "Flat":
		return TrendStable
	case "FortyFiveDown":
		return TrendFalling
	case "SingleDown", "DoubleDown":
		return TrendFallingFast
	}
	return TrendUnknown
}

// NightscoutDirection is the reverse of TrendFromNightscout.
func NightscoutDirection(trend string) string {
	switch trend {
	case TrendRisingFast:
		return "SingleUp"
	case TrendRising:
		return "FortyFiveUp"
	case TrendStable:
		return "Flat"
	case TrendFalling:
		return "FortyFiveDown"
	case TrendFallingFast:
		return "SingleDown"
	}
	return "NONE"
}
