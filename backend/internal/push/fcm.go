// Package push sends notifications through Firebase Cloud Messaging (HTTP v1)
// and runs the outbox of alert pushes (SRS UC116, UC009).
//
// It calls the FCM REST API directly, with a service-account token from
// golang.org/x/oauth2/jwt, instead of the Firebase Admin SDK: the SDK's
// recent versions need Go 1.25 and pull in dozens of modules.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"
)

// Errors of a send.
var (
	// ErrInvalidToken: the app was uninstalled or the token expired. Stop using it.
	ErrInvalidToken = errors.New("fcm: the token is no longer valid")
	// ErrRetryable: FCM is overloaded or failed. Try again later.
	ErrRetryable = errors.New("fcm: temporary failure")
	// ErrNotConfigured: no Firebase service account on this server.
	ErrNotConfigured = errors.New("push notifications are not configured on this server")
)

// Sender sends one data message to one device.
type Sender interface {
	Send(ctx context.Context, token string, data map[string]string, highPriority bool) (messageID string, err error)
}

// FCM is the Firebase Cloud Messaging HTTP v1 client.
type FCM struct {
	projectID string
	endpoint  string
	client    *http.Client
}

const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

// NewFCM reads a Firebase service-account key (the JSON downloaded from the
// Firebase console) and returns a client that refreshes its access token.
func NewFCM(ctx context.Context, serviceAccountJSON []byte) (*FCM, error) {
	var sa struct {
		ProjectID   string `json:"project_id"`
		PrivateKey  string `json:"private_key"`
		ClientEmail string `json:"client_email"`
		TokenURI    string `json:"token_uri"`
	}
	if err := json.Unmarshal(serviceAccountJSON, &sa); err != nil {
		return nil, fmt.Errorf("firebase service account: %w", err)
	}
	if sa.ProjectID == "" || sa.PrivateKey == "" || sa.ClientEmail == "" {
		return nil, errors.New("firebase service account: project_id, private_key and client_email are required")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	conf := &jwt.Config{Email: sa.ClientEmail, PrivateKey: []byte(sa.PrivateKey), Scopes: []string{fcmScope}, TokenURL: sa.TokenURI}
	base := &http.Client{Timeout: 10 * time.Second}
	tokens := conf.TokenSource(context.WithValue(ctx, oauth2.HTTPClient, base))
	return &FCM{
		projectID: sa.ProjectID,
		endpoint:  "https://fcm.googleapis.com/v1/projects/" + sa.ProjectID + "/messages:send",
		client:    &http.Client{Timeout: 10 * time.Second, Transport: &oauth2.Transport{Source: tokens, Base: base.Transport}},
	}, nil
}

// Send sends a data message. High priority wakes the phone at once (critical alerts).
func (f *FCM) Send(ctx context.Context, token string, data map[string]string, highPriority bool) (string, error) {
	priority, urgency := "NORMAL", "normal"
	if highPriority {
		priority, urgency = "HIGH", "high"
	}
	body, err := json.Marshal(map[string]any{
		"message": map[string]any{
			"token":   token,
			"data":    data,
			"android": map[string]any{"priority": priority},
			"webpush": map[string]any{"headers": map[string]string{"Urgency": urgency}},
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrRetryable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusOK {
		var ok struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(raw, &ok)
		return ok.Name, nil
	}
	return "", classify(resp.StatusCode, raw)
}

// classify maps an FCM error to ErrInvalidToken, ErrRetryable or a permanent error.
// https://firebase.google.com/docs/reference/fcm/rest/v1/ErrorCode
func classify(status int, raw []byte) error {
	var e struct {
		Error struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Details []struct {
				ErrorCode string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &e)
	code := e.Error.Status
	for _, d := range e.Error.Details {
		if d.ErrorCode != "" {
			code = d.ErrorCode
		}
	}
	switch {
	case code == "UNREGISTERED" || status == http.StatusNotFound:
		return ErrInvalidToken
	case code == "INVALID_ARGUMENT" && status == http.StatusBadRequest:
		return fmt.Errorf("%w: %s", ErrInvalidToken, e.Error.Message)
	case status == http.StatusTooManyRequests || status >= 500 || code == "UNAVAILABLE" || code == "INTERNAL" || code == "QUOTA_EXCEEDED":
		return fmt.Errorf("%w: %d %s", ErrRetryable, status, code)
	}
	return fmt.Errorf("fcm: %d %s: %s", status, code, e.Error.Message)
}

// Disabled is the sender of a server without Firebase credentials: every
// send fails with ErrNotConfigured, so alerts fall back to the escalation.
type Disabled struct{}

func (Disabled) Send(context.Context, string, map[string]string, bool) (string, error) {
	return "", ErrNotConfigured
}
