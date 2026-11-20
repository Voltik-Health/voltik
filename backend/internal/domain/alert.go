package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Alert types, severities and states (alerts).
const (
	AlertCriticalLow        = "critical_low"
	AlertCriticalHigh       = "critical_high"
	AlertPredictedLow       = "predicted_low"
	AlertPredictedHigh      = "predicted_high"
	AlertSensorDisconnected = "sensor_disconnected"
	AlertSensorExpiring     = "sensor_expiring"

	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"

	AlertPending      = "pending"
	AlertAcknowledged = "acknowledged"
	AlertResolved     = "resolved"
	AlertDismissed    = "dismissed"
)

// Notification channels and delivery states (alert_recipients).
const (
	ChannelPush  = "push"
	ChannelSMS   = "sms"
	ChannelEmail = "email"
	ChannelInApp = "in_app"

	DeliveryPending      = "pending"
	DeliverySent         = "sent"
	DeliveryDelivered    = "delivered"
	DeliveryRead         = "read"
	DeliveryAcknowledged = "acknowledged"
	DeliveryFailed       = "failed"
)

// Alert rules (SRS UC114, UC117, UC092).
const (
	// A pending alert of the same type in this window is updated instead of repeated.
	AlertRepeatWindow = 30 * time.Minute
	// A critical alert still pending after this is escalated by SMS.
	EscalationDelay = 3 * time.Minute
	// A sensor without data for longer than this is disconnected.
	SensorSilence = 20 * time.Minute
	// A sensor expiring within this gets a warning.
	SensorExpiryNotice = 24 * time.Hour
)

// Alert is a row of alerts, with the patient found through its source.
type Alert struct {
	ID                  uuid.UUID
	PatientID           uuid.UUID
	Type                string
	Severity            string
	Title               string
	Message             string
	IrisSuggestedAction *string
	TriggeredAt         time.Time
	AckStatus           string
	AcknowledgedAt      *time.Time
	ResolutionNotes     *string
	SourceReadingID     *int64
	SourcePredictionID  *int64
	DeviceID            *uuid.UUID
	SourceValueMgDl     *int
	AcknowledgedBy      *Actor
	Recipients          []AlertRecipient
	CreatedAt           time.Time
}

// Closed reports whether the alert was resolved or dismissed.
func (a Alert) Closed() bool {
	return a.AckStatus == AlertResolved || a.AckStatus == AlertDismissed
}

// Actor is a person shown next to an action.
type Actor struct {
	UserID   uuid.UUID
	FullName string
}

// AlertRecipient is one person and one channel of an alert.
type AlertRecipient struct {
	ID             uuid.UUID
	AlertID        uuid.UUID
	UserID         uuid.UUID
	FullName       string
	Channel        string
	DeliveryStatus string
	SentAt         *time.Time
	DeliveredAt    *time.Time
	ReadAt         *time.Time
	AcknowledgedAt *time.Time
}

// AlertFilter narrows the alert history.
type AlertFilter struct {
	AckStatus *string
	Type      *string
	Severity  *string
	From      *time.Time
	To        *time.Time
	Limit     int
	Cursor    string
}

// NewAlert is an alert about to be raised.
type NewAlert struct {
	PatientID       uuid.UUID
	Type            string
	Severity        string
	Title           string
	Message         string
	SuggestedAction *string
	TriggeredAt     time.Time
	SourceReadingID *int64
	DeviceID        *uuid.UUID
	// Escalate: critical alerts are escalated by SMS if nobody acknowledges them.
	Escalate bool
}

// CriticalAlertFor checks a reading against the patient's critical limits
// (at or below the low limit, at or above the high one). It returns false when
// the value is inside them.
func CriticalAlertFor(r GlucoseReading, t Thresholds) (NewAlert, bool) {
	var a NewAlert
	switch {
	case r.ValueMgDl <= t.CriticalLow:
		a.Type, a.Title = AlertCriticalLow, "Glicemia muito baixa"
		a.SuggestedAction = ptrTo("Siga o seu plano para hipoglicemia e volte a medir dentro de 15 minutos.")
	case r.ValueMgDl >= t.CriticalHigh:
		a.Type, a.Title = AlertCriticalHigh, "Glicemia muito alta"
		a.SuggestedAction = ptrTo("Siga o plano que combinou com a sua equipa de saúde para glicemias altas.")
	default:
		return a, false
	}
	id := r.ID
	a.PatientID = r.PatientID
	a.Severity = SeverityCritical
	a.Message = CriticalAlertMessage(r.ValueMgDl, r.Trend)
	a.TriggeredAt = r.MeasuredAt
	a.SourceReadingID = &id
	a.Escalate = true
	return a, true
}

// CriticalAlertMessage is the text of a critical alert, in Portuguese.
func CriticalAlertMessage(value int, trend string) string {
	suffix := map[string]string{
		TrendRisingFast:  " e a subir depressa",
		TrendRising:      " e a subir",
		TrendFalling:     " e a descer",
		TrendFallingFast: " e a descer depressa",
	}[trend]
	return fmt.Sprintf("A glicemia está em %d mg/dL%s.", value, suffix)
}

// SensorDisconnectedAlert is the warning for a sensor without data.
func SensorDisconnectedAlert(d Device, now time.Time) NewAlert {
	id := d.ID
	return NewAlert{
		PatientID:   d.PatientID,
		Type:        AlertSensorDisconnected,
		Severity:    SeverityWarning,
		Title:       "Sensor sem dados",
		Message:     "O sensor não envia leituras há mais de 20 minutos. Confirme que está perto do telemóvel e com o Bluetooth ligado.",
		TriggeredAt: now,
		DeviceID:    &id,
	}
}

// SensorExpiringAlert is the notice for a sensor about to expire.
func SensorExpiringAlert(d Device, now time.Time) NewAlert {
	id := d.ID
	when := "em breve"
	if d.SensorExpiresAt != nil {
		exp := d.SensorExpiresAt.In(Lisbon)
		day := "amanhã"
		if LisbonDate(exp).Equal(LisbonDate(now)) {
			day = "hoje"
		}
		when = fmt.Sprintf("%s às %s", day, exp.Format("15:04"))
	}
	return NewAlert{
		PatientID:   d.PatientID,
		Type:        AlertSensorExpiring,
		Severity:    SeverityInfo,
		Title:       "Sensor a expirar",
		Message:     "O sensor expira " + when + ". Tenha um sensor novo à mão.",
		TriggeredAt: now,
		DeviceID:    &id,
	}
}

func ptrTo[T any](v T) *T { return &v }
