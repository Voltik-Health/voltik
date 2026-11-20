package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// PushToken is a row of notification_tokens.
type PushToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Platform  string
	Token     string
	IsActive  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// PushJob is an alert push waiting to be sent to one person.
type PushJob struct {
	RecipientID uuid.UUID
	AlertID     uuid.UUID
	UserID      uuid.UUID
	Attempts    int
	AlertType   string
	Severity    string
	Title       string
	Message     string
	AckStatus   string
	// The patient's name goes in the push of caregivers.
	PatientUserID uuid.UUID
	PatientName   string
	Tokens        []PushToken
}

// PushBody is the text of the notification for this person.
func (j PushJob) PushBody() string {
	if j.UserID == j.PatientUserID {
		return j.Message
	}
	return j.PatientName + ": " + j.Message
}

// SMSJob is an escalation SMS waiting to be sent.
type SMSJob struct {
	ID       uuid.UUID
	Phone    string
	Body     string
	Attempts int
}

// Escalation limits (UC117).
const (
	EscalationInterval = 3 * time.Minute
	MaxSMSAttempts     = 3
	MaxPushAttempts    = 5
)

// EscalationText is the SMS of an escalation, in Portuguese. It has only the
// value, the time and the patient's name: never a diagnosis or a dose.
func EscalationText(patientName, alertType string, value *int, at time.Time, toCaregiver bool) string {
	what := "a glicemia fora dos limites"
	switch alertType {
	case AlertCriticalLow:
		what = "a glicemia muito baixa"
	case AlertCriticalHigh:
		what = "a glicemia muito alta"
	}
	reading := ""
	if value != nil {
		reading = fmt.Sprintf(" (%d mg/dL às %s)", *value, at.In(Lisbon).Format("15:04"))
	}
	if toCaregiver {
		return fmt.Sprintf("Voltik: %s tem %s%s e ainda ninguém confirmou o alerta. Abra a app Voltik para confirmar.",
			patientName, what, reading)
	}
	return fmt.Sprintf("Voltik: %s tem %s%s e ainda ninguém confirmou o alerta. Tente falar com %s.",
		patientName, what, reading, patientName)
}
