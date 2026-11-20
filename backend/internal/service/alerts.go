package service

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

// AlertStore is what the alert service needs from the database.
type AlertStore interface {
	ListAlerts(ctx context.Context, patientID uuid.UUID, f domain.AlertFilter) ([]domain.Alert, string, error)
	AcknowledgeAlert(ctx context.Context, alertID, userID uuid.UUID) (domain.Alert, bool, error)
	ResolveAlert(ctx context.Context, alertID, userID uuid.UUID, status string, notes *string) (domain.Alert, bool, error)
	MarkNotification(ctx context.Context, recipientID, userID uuid.UUID, status string) error
	AlertUsers(ctx context.Context, alertID uuid.UUID, except uuid.UUID) ([]uuid.UUID, error)
	RaiseAlert(ctx context.Context, n domain.NewAlert, now time.Time) (domain.Alert, bool, error)
	DisconnectSilentSensors(ctx context.Context, now time.Time) ([]domain.Device, error)
	ExpiringSensors(ctx context.Context, now time.Time) ([]domain.Device, error)
	ExpireSensors(ctx context.Context, now time.Time) (int64, error)
	EscalateNext(ctx context.Context, now time.Time) (bool, error)
}

// Notifier sends informative pushes (best effort).
type Notifier interface {
	Notify(ctx context.Context, userIDs []uuid.UUID, data map[string]string)
}

// Alerts runs the alert history, acknowledgements and receipts (UC118–UC121),
// and the jobs of sensor monitoring (UC092) and escalation (UC117).
type Alerts struct {
	store    AlertStore
	notifier Notifier
	push     Waker
	log      *slog.Logger
	now      func() time.Time
	async    func(func()) // runs the notifications in the background (synchronous in tests)
}

func NewAlerts(store AlertStore, notifier Notifier, push Waker, log *slog.Logger) *Alerts {
	return &Alerts{store: store, notifier: notifier, push: push, log: log, now: time.Now, async: func(f func()) { go f() }}
}

// List returns the patient's alerts, newest first (UC120).
func (s *Alerts) List(ctx context.Context, a domain.Account, f domain.AlertFilter) ([]domain.Alert, string, error) {
	pid, err := patientOf(a)
	if err != nil {
		return nil, "", err
	}
	if f.From != nil && f.To != nil && !f.To.After(*f.From) {
		return nil, "", domain.Invalid("to", "must be after from")
	}
	return s.store.ListAlerts(ctx, pid, f)
}

// Acknowledge records that the caller, a recipient of the alert, saw it (UC118).
func (s *Alerts) Acknowledge(ctx context.Context, a domain.Account, alertID uuid.UUID) (domain.Alert, error) {
	alert, changed, err := s.store.AcknowledgeAlert(ctx, alertID, a.ID)
	if err == nil && changed {
		s.tellOthers(alert, a.ID)
	}
	return alert, err
}

// Resolve closes the alert as resolved or dismissed (UC119).
func (s *Alerts) Resolve(ctx context.Context, a domain.Account, alertID uuid.UUID, status string, notes *string) (domain.Alert, error) {
	if status != domain.AlertResolved && status != domain.AlertDismissed {
		return domain.Alert{}, domain.Invalid("status", "must be resolved or dismissed")
	}
	if notes != nil {
		trimmed := strings.TrimSpace(*notes)
		switch {
		case trimmed == "":
			notes = nil
		case utf8.RuneCountInString(trimmed) > 255:
			return domain.Alert{}, domain.Invalid("resolution_notes", "must be at most 255 characters")
		default:
			notes = &trimmed
		}
	}
	alert, changed, err := s.store.ResolveAlert(ctx, alertID, a.ID, status, notes)
	if err == nil && changed {
		s.tellOthers(alert, a.ID)
	}
	return alert, err
}

// tellOthers lets the other recipients hide an alert someone handled (UC118 step 5).
func (s *Alerts) tellOthers(alert domain.Alert, by uuid.UUID) {
	s.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		users, err := s.store.AlertUsers(ctx, alert.ID, by)
		if err != nil {
			s.log.Warn("alerts: could not read the other recipients", "error", err)
			return
		}
		s.notifier.Notify(ctx, users, map[string]string{
			"type": "alert_closed", "alert_id": alert.ID.String(), "ack_status": alert.AckStatus,
		})
	})
}

// MarkNotification records that a notification reached the phone or was opened (UC121).
func (s *Alerts) MarkNotification(ctx context.Context, a domain.Account, recipientID uuid.UUID, status string) error {
	return s.store.MarkNotification(ctx, recipientID, a.ID, status)
}

// CheckSensors is the job of UC092 and UC078 A1, every 5 minutes: sensors past
// their end of life become expired, active sensors without data for 20
// minutes become disconnected with a warning, and sensors about to expire
// get a notice.
func (s *Alerts) CheckSensors(ctx context.Context) {
	now := s.now()
	if _, err := s.store.ExpireSensors(ctx, now); err != nil {
		s.log.Warn("sensor monitor: could not expire sensors", "error", err)
	}
	silent, err := s.store.DisconnectSilentSensors(ctx, now)
	if err != nil {
		s.log.Warn("sensor monitor: could not check silent sensors", "error", err)
	}
	expiring, err := s.store.ExpiringSensors(ctx, now)
	if err != nil {
		s.log.Warn("sensor monitor: could not check expiring sensors", "error", err)
	}
	raised := 0
	for _, d := range silent {
		if _, created, err := s.store.RaiseAlert(ctx, domain.SensorDisconnectedAlert(d, now), now); err != nil {
			s.log.Warn("sensor monitor: could not raise an alert", "error", err)
		} else if created {
			raised++
		}
	}
	for _, d := range expiring {
		if _, created, err := s.store.RaiseAlert(ctx, domain.SensorExpiringAlert(d, now), now); err != nil {
			s.log.Warn("sensor monitor: could not raise an alert", "error", err)
		} else if created {
			raised++
		}
	}
	if raised > 0 && s.push != nil {
		s.push.Wake()
	}
}

// Escalate runs the due escalation steps of critical alerts (UC117).
func (s *Alerts) Escalate(ctx context.Context) {
	for i := 0; i < 100 && ctx.Err() == nil; i++ {
		found, err := s.store.EscalateNext(ctx, s.now())
		if err != nil {
			s.log.Warn("escalation: step failed", "error", err)
			return
		}
		if !found {
			return
		}
	}
}
