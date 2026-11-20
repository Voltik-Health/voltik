// Package sms sends the escalation texts of critical alerts (SRS UC117) and
// checks the delivery receipts (UC121).
//
// Until the project has an SMS provider account, the Log sender only writes
// to the API log, so the escalation can be seen working without sending
// anything. With SMS_PROVIDER=twilio the texts go through Twilio.
package sms

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

// ErrRetryable is a temporary failure of the provider: try again later.
var ErrRetryable = errors.New("sms: temporary failure")

// Sender sends one text.
type Sender interface {
	Name() string // stored in sms_messages.provider
	Send(ctx context.Context, to, body string) (messageID string, err error)
}

// Log writes the text to the log instead of sending it. The phone number is
// masked and the body is left out, because it has health data.
type Log struct{ Logger *slog.Logger }

func (Log) Name() string { return "log" }

func (l Log) Send(_ context.Context, to, body string) (string, error) {
	l.Logger.Info("sms not sent: no SMS provider configured (SMS_PROVIDER=log)",
		"to", mask(to), "characters", len([]rune(body)))
	return "", nil
}

func mask(phone string) string {
	if len(phone) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(phone)-4) + phone[len(phone)-4:]
}

// Twilio sends through the Twilio Messages API.
type Twilio struct {
	AccountSID     string
	AuthToken      string
	From           string // a Twilio number or an alphanumeric sender id
	StatusCallback string // public URL of POST /v1/webhooks/sms
	Client         *http.Client
	BaseURL        string // tests only
}

func (Twilio) Name() string { return "twilio" }

func (t Twilio) Send(ctx context.Context, to, body string) (string, error) {
	base := t.BaseURL
	if base == "" {
		base = "https://api.twilio.com"
	}
	form := url.Values{"To": {to}, "From": {t.From}, "Body": {body}}
	if t.StatusCallback != "" {
		form.Set("StatusCallback", t.StatusCallback)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/2010-04-01/Accounts/"+url.PathEscape(t.AccountSID)+"/Messages.json", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(t.AccountSID, t.AuthToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrRetryable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
	var out struct {
		SID     string `json:"sid"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &out)
	switch {
	case resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK:
		return out.SID, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return "", fmt.Errorf("%w: twilio %d", ErrRetryable, resp.StatusCode)
	}
	return "", fmt.Errorf("twilio %d (code %d): %s", resp.StatusCode, out.Code, out.Message)
}

// ValidTwilioSignature checks the X-Twilio-Signature of a webhook: the
// base64 HMAC-SHA1 of the full URL followed by every POST field (name and
// value) in alphabetical order, keyed with the auth token.
func ValidTwilioSignature(authToken, fullURL string, form url.Values, signature string) bool {
	keys := make([]string, 0, len(form))
	for k := range form {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(fullURL)
	for _, k := range keys {
		for _, v := range form[k] {
			b.WriteString(k)
			b.WriteString(v)
		}
	}
	mac := hmac.New(sha1.New, []byte(authToken))
	mac.Write([]byte(b.String()))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

// Store is what the SMS outbox needs from the database.
type Store interface {
	ClaimSMSJobs(ctx context.Context, now time.Time, limit int) ([]domain.SMSJob, error)
	SMSSent(ctx context.Context, id uuid.UUID, provider, providerID string) error
	SMSRetry(ctx context.Context, id uuid.UUID, retryAt time.Time, reason string) error
	SMSFailed(ctx context.Context, id uuid.UUID, reason string) error
}

// Outbox sends the pending escalation texts.
type Outbox struct {
	store  Store
	sender Sender
	log    *slog.Logger
	now    func() time.Time
}

func NewOutbox(store Store, sender Sender, log *slog.Logger) *Outbox {
	return &Outbox{store: store, sender: sender, log: log, now: time.Now}
}

// Drain sends every text that is due. A failed text is tried 3 times, then
// marked failed (UC117 A1); the escalation goes on with the next contact.
func (o *Outbox) Drain(ctx context.Context) {
	for ctx.Err() == nil {
		jobs, err := o.store.ClaimSMSJobs(ctx, o.now(), 20)
		if err != nil {
			o.log.Warn("sms outbox: could not read pending texts", "error", err)
			return
		}
		for _, j := range jobs {
			o.send(ctx, j)
		}
		if len(jobs) < 20 {
			return
		}
	}
}

func (o *Outbox) send(ctx context.Context, j domain.SMSJob) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	id, err := o.sender.Send(ctx, j.Phone, j.Body)
	var recordErr error
	switch {
	case err == nil:
		recordErr = o.store.SMSSent(ctx, j.ID, o.sender.Name(), id)
	case j.Attempts < domain.MaxSMSAttempts:
		recordErr = o.store.SMSRetry(ctx, j.ID, o.now().Add(time.Duration(j.Attempts)*20*time.Second), err.Error())
	default:
		o.log.Warn("sms not delivered after 3 attempts", "error", err)
		recordErr = o.store.SMSFailed(ctx, j.ID, err.Error())
	}
	if recordErr != nil {
		o.log.Warn("sms outbox: could not record the result", "error", recordErr)
	}
}
