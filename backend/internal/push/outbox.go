package push

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

// Store is what the outbox needs from the database.
type Store interface {
	ClaimPushJobs(ctx context.Context, now time.Time, limit int) ([]domain.PushJob, error)
	PushSent(ctx context.Context, recipientID uuid.UUID, providerID string) error
	PushRetry(ctx context.Context, recipientID uuid.UUID, retryAt time.Time, reason string) error
	PushFailed(ctx context.Context, recipientID uuid.UUID, reason string) error
	ActiveTokens(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID][]domain.PushToken, error)
	DeactivatePushToken(ctx context.Context, tokenID uuid.UUID) error
}

// Outbox sends the pending alert pushes of alert_recipients. Alerts are
// written to the database first and pushed afterwards, so a push that fails
// is retried and a restart loses nothing.
type Outbox struct {
	store  Store
	sender Sender
	log    *slog.Logger
	wake   chan struct{}
	now    func() time.Time
}

const (
	pollInterval = 2 * time.Second
	claimBatch   = 20
	sendTimeout  = 15 * time.Second
)

func NewOutbox(store Store, sender Sender, log *slog.Logger) *Outbox {
	return &Outbox{store: store, sender: sender, log: log, wake: make(chan struct{}, 1), now: time.Now}
}

// Wake makes the outbox look for pushes now instead of at the next tick
// (called right after an alert is raised: RNF08 asks for less than a minute).
func (o *Outbox) Wake() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

// Run sends pending pushes until ctx is cancelled.
func (o *Outbox) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-o.wake:
		}
		o.Drain(ctx)
	}
}

// Drain sends every push that is due now.
func (o *Outbox) Drain(ctx context.Context) {
	for ctx.Err() == nil {
		jobs, err := o.store.ClaimPushJobs(ctx, o.now(), claimBatch)
		if err != nil {
			o.log.Warn("push outbox: could not read pending pushes", "error", err)
			return
		}
		for _, j := range jobs {
			o.send(ctx, j)
		}
		if len(jobs) < claimBatch {
			return
		}
	}
}

func (o *Outbox) send(ctx context.Context, j domain.PushJob) {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	record := func(err error) {
		if err != nil {
			o.log.Warn("push outbox: could not record the result", "error", err)
		}
	}
	if j.AckStatus != domain.AlertPending {
		record(o.store.PushFailed(ctx, j.RecipientID, "the alert was closed before the push was sent"))
		return
	}
	if len(j.Tokens) == 0 {
		record(o.store.PushFailed(ctx, j.RecipientID, "no active push token"))
		return
	}
	data := map[string]string{
		"type":         "alert",
		"alert_id":     j.AlertID.String(),
		"recipient_id": j.RecipientID.String(),
		"alert_type":   j.AlertType,
		"severity":     j.Severity,
		"title":        j.Title,
		"body":         j.PushBody(),
	}
	high := j.Severity == domain.SeverityCritical
	var sentID string
	var lastErr error
	retry := false
	for _, t := range j.Tokens {
		id, err := o.sender.Send(ctx, t.Token, data, high)
		switch {
		case err == nil:
			if sentID == "" {
				sentID = id
			}
		case errors.Is(err, ErrInvalidToken):
			record(o.store.DeactivatePushToken(ctx, t.ID))
			lastErr = err
		case errors.Is(err, ErrRetryable):
			retry, lastErr = true, err
		default:
			lastErr = err
		}
	}
	switch {
	case sentID != "" || (lastErr == nil && len(j.Tokens) > 0):
		record(o.store.PushSent(ctx, j.RecipientID, sentID))
	case retry && j.Attempts < domain.MaxPushAttempts:
		// UC009: exponential backoff, 1 s, 2 s, 4 s...
		record(o.store.PushRetry(ctx, j.RecipientID, o.now().Add(time.Second<<(j.Attempts-1)), lastErr.Error()))
	default:
		record(o.store.PushFailed(ctx, j.RecipientID, lastErr.Error()))
	}
}

// Notify sends an informative push (not an alert: a photo analysed, a report
// ready, an alert closed) to people's phones. Best effort: nothing is retried.
func (o *Outbox) Notify(ctx context.Context, userIDs []uuid.UUID, data map[string]string) {
	if len(userIDs) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	tokens, err := o.store.ActiveTokens(ctx, userIDs)
	if err != nil {
		o.log.Warn("push: could not read the tokens", "error", err)
		return
	}
	for _, list := range tokens {
		for _, t := range list {
			_, err := o.sender.Send(ctx, t.Token, data, false)
			switch {
			case errors.Is(err, ErrInvalidToken):
				_ = o.store.DeactivatePushToken(ctx, t.ID)
			case errors.Is(err, ErrNotConfigured):
				return
			case err != nil:
				o.log.Warn("push: notification not sent", "type", data["type"], "error", err)
			}
		}
	}
}
