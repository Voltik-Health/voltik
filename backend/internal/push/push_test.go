package push

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"unregistered", 404, `{"error":{"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`, ErrInvalidToken},
		{"bad token", 400, `{"error":{"status":"INVALID_ARGUMENT","message":"The registration token is not a valid FCM registration token"}}`, ErrInvalidToken},
		{"quota", 429, `{"error":{"status":"RESOURCE_EXHAUSTED","details":[{"errorCode":"QUOTA_EXCEEDED"}]}}`, ErrRetryable},
		{"unavailable", 503, `{"error":{"status":"UNAVAILABLE"}}`, ErrRetryable},
		{"sender mismatch", 403, `{"error":{"status":"PERMISSION_DENIED","details":[{"errorCode":"SENDER_ID_MISMATCH"}]}}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classify(tt.status, []byte(tt.body))
			switch {
			case tt.want == nil && (errors.Is(err, ErrInvalidToken) || errors.Is(err, ErrRetryable)):
				t.Fatalf("err = %v, want a permanent error", err)
			case tt.want != nil && !errors.Is(err, tt.want):
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

type fakeStore struct {
	jobs        []domain.PushJob
	sent        map[uuid.UUID]string
	retried     map[uuid.UUID]time.Time
	failed      map[uuid.UUID]string
	deactivated []uuid.UUID
}

func (f *fakeStore) ClaimPushJobs(context.Context, time.Time, int) ([]domain.PushJob, error) {
	j := f.jobs
	f.jobs = nil
	return j, nil
}
func (f *fakeStore) PushSent(_ context.Context, id uuid.UUID, provider string) error {
	f.sent[id] = provider
	return nil
}
func (f *fakeStore) PushRetry(_ context.Context, id uuid.UUID, at time.Time, _ string) error {
	f.retried[id] = at
	return nil
}
func (f *fakeStore) PushFailed(_ context.Context, id uuid.UUID, reason string) error {
	f.failed[id] = reason
	return nil
}
func (f *fakeStore) ActiveTokens(context.Context, []uuid.UUID) (map[uuid.UUID][]domain.PushToken, error) {
	return nil, nil
}
func (f *fakeStore) DeactivatePushToken(_ context.Context, id uuid.UUID) error {
	f.deactivated = append(f.deactivated, id)
	return nil
}

// fakeSender answers by token.
type fakeSender map[string]error

func (f fakeSender) Send(_ context.Context, token string, data map[string]string, high bool) (string, error) {
	if data["type"] != "alert" || data["recipient_id"] == "" {
		return "", errors.New("missing data")
	}
	if err := f[token]; err != nil {
		return "", err
	}
	return "projects/p/messages/" + token, nil
}

func job(severity string, attempts int, tokens ...string) domain.PushJob {
	j := domain.PushJob{RecipientID: uuid.New(), AlertID: uuid.New(), UserID: uuid.New(), Attempts: attempts,
		AlertType: domain.AlertCriticalLow, Severity: severity, Title: "Glicemia muito baixa", Message: "A glicemia está em 52 mg/dL.",
		AckStatus: domain.AlertPending}
	j.PatientUserID = j.UserID
	for _, tk := range tokens {
		j.Tokens = append(j.Tokens, domain.PushToken{ID: uuid.New(), Token: tk})
	}
	return j
}

func TestOutboxSendsRetriesAndGivesUp(t *testing.T) {
	ok, dead, flaky, none, closed, exhausted := job("critical", 1, "good", "dead"), job("critical", 1, "dead"),
		job("critical", 1, "flaky"), job("critical", 1), job("critical", 1, "good"), job("critical", domain.MaxPushAttempts, "flaky")
	closed.AckStatus = domain.AlertAcknowledged
	store := &fakeStore{jobs: []domain.PushJob{ok, dead, flaky, none, closed, exhausted},
		sent: map[uuid.UUID]string{}, retried: map[uuid.UUID]time.Time{}, failed: map[uuid.UUID]string{}}
	sender := fakeSender{"dead": ErrInvalidToken, "flaky": ErrRetryable}
	o := NewOutbox(store, sender, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	o.now = func() time.Time { return now }
	o.Drain(context.Background())

	if store.sent[ok.RecipientID] != "projects/p/messages/good" {
		t.Fatalf("one good token is enough: %v", store.sent)
	}
	if len(store.deactivated) != 2 {
		t.Fatalf("both dead tokens are deactivated: %v", store.deactivated)
	}
	if _, failed := store.failed[dead.RecipientID]; !failed {
		t.Fatal("a recipient whose only token is dead fails")
	}
	if at := store.retried[flaky.RecipientID]; !at.Equal(now.Add(time.Second)) {
		t.Fatalf("first retry after 1 s, got %v", at)
	}
	if store.failed[none.RecipientID] != "no active push token" {
		t.Fatalf("no tokens: %v", store.failed[none.RecipientID])
	}
	if _, failed := store.failed[closed.RecipientID]; !failed {
		t.Fatal("an alert closed before sending is not pushed")
	}
	if _, failed := store.failed[exhausted.RecipientID]; !failed {
		t.Fatal("after the last attempt the push fails")
	}
}

func TestDisabledSender(t *testing.T) {
	if _, err := (Disabled{}).Send(context.Background(), "t", nil, true); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v", err)
	}
	_ = http.StatusOK
}
