package sms

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

func TestTwilioSignature(t *testing.T) {
	// The algorithm of https://www.twilio.com/docs/usage/webhooks/webhooks-security
	form := url.Values{"MessageSid": {"SM123"}, "MessageStatus": {"delivered"}, "AccountSid": {"AC1"}}
	full := "https://voltik-cgm-staging.duckdns.org/v1/webhooks/sms"
	mac := hmac.New(sha1.New, []byte("secret"))
	mac.Write([]byte(full + "AccountSidAC1" + "MessageSidSM123" + "MessageStatusdelivered"))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !ValidTwilioSignature("secret", full, form, sig) {
		t.Fatal("a valid signature was refused")
	}
	if ValidTwilioSignature("other", full, form, sig) || ValidTwilioSignature("secret", full+"?x=1", form, sig) {
		t.Fatal("a wrong signature was accepted")
	}
}

func TestTwilioSend(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		if r.URL.Path != "/2010-04-01/Accounts/AC1/Messages.json" || user != "AC1" || pass != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = r.ParseForm()
		got = r.PostForm
		if got.Get("To") == "+351900000000" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":21211,"message":"invalid number"}`))
			return
		}
		if got.Get("To") == "+351911111111" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"sid":"SM42","status":"queued"}`))
	}))
	defer srv.Close()
	tw := Twilio{AccountSID: "AC1", AuthToken: "tok", From: "Voltik", StatusCallback: "https://x/v1/webhooks/sms", BaseURL: srv.URL}

	id, err := tw.Send(context.Background(), "+351912345678", "Olá")
	if err != nil || id != "SM42" || got.Get("Body") != "Olá" || got.Get("From") != "Voltik" || got.Get("StatusCallback") == "" {
		t.Fatalf("send: %q %v %v", id, err, got)
	}
	if _, err := tw.Send(context.Background(), "+351900000000", "x"); err == nil || errors.Is(err, ErrRetryable) {
		t.Fatalf("an invalid number is a permanent error: %v", err)
	}
	if _, err := tw.Send(context.Background(), "+351911111111", "x"); !errors.Is(err, ErrRetryable) {
		t.Fatalf("a 503 is retried: %v", err)
	}
}

type fakeStore struct {
	jobs                []domain.SMSJob
	sent, failed, retry []uuid.UUID
}

func (f *fakeStore) ClaimSMSJobs(context.Context, time.Time, int) ([]domain.SMSJob, error) {
	j := f.jobs
	f.jobs = nil
	return j, nil
}
func (f *fakeStore) SMSSent(_ context.Context, id uuid.UUID, _, _ string) error {
	f.sent = append(f.sent, id)
	return nil
}
func (f *fakeStore) SMSRetry(_ context.Context, id uuid.UUID, _ time.Time, _ string) error {
	f.retry = append(f.retry, id)
	return nil
}
func (f *fakeStore) SMSFailed(_ context.Context, id uuid.UUID, _ string) error {
	f.failed = append(f.failed, id)
	return nil
}

type failing struct{}

func (failing) Name() string                                         { return "failing" }
func (failing) Send(context.Context, string, string) (string, error) { return "", ErrRetryable }

func TestOutboxTriesThreeTimes(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	first, last := domain.SMSJob{ID: uuid.New(), Phone: "+351912345678", Body: "x", Attempts: 1},
		domain.SMSJob{ID: uuid.New(), Phone: "+351912345678", Body: "x", Attempts: domain.MaxSMSAttempts}
	store := &fakeStore{jobs: []domain.SMSJob{first, last}}
	NewOutbox(store, failing{}, log).Drain(context.Background())
	if len(store.retry) != 1 || store.retry[0] != first.ID || len(store.failed) != 1 || store.failed[0] != last.ID {
		t.Fatalf("retry %v failed %v", store.retry, store.failed)
	}

	store = &fakeStore{jobs: []domain.SMSJob{first}}
	NewOutbox(store, Log{Logger: log}, log).Drain(context.Background())
	if len(store.sent) != 1 {
		t.Fatal("the log sender always succeeds")
	}
}

func TestMask(t *testing.T) {
	if got := mask("+351912345678"); got != "*********5678" {
		t.Fatalf("mask = %q", got)
	}
}
