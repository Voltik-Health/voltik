package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/voltik/voltik/backend/internal/auth"
	"github.com/voltik/voltik/backend/internal/database/dbtest"
	"github.com/voltik/voltik/backend/internal/domain"
	"github.com/voltik/voltik/backend/internal/handler"
	"github.com/voltik/voltik/backend/internal/repository"
	"github.com/voltik/voltik/backend/internal/service"
)

// Integration tests: the whole API (handlers, services, repository) against
// PostgreSQL, with a fake Google that accepts any token "google:<subject>".
func TestMain(m *testing.M) { dbtest.Main(m) }

type fakeGoogle struct{}

func (fakeGoogle) Verify(_ context.Context, token string) (domain.Identity, error) {
	subject, ok := strings.CutPrefix(token, "google:")
	if !ok {
		return domain.Identity{}, domain.ErrInvalidProviderToken
	}
	email := subject + "@example.com"
	return domain.Identity{Provider: "google", Subject: subject, Email: &email, FullName: "Test " + subject}, nil
}

type client struct {
	t     *testing.T
	api   http.Handler
	token string
}

func newAPI(t *testing.T) http.Handler {
	store := repository.New(dbtest.API(t))
	tokens := auth.NewTokens([]byte("0123456789abcdef0123456789abcdef"), "local")
	return handler.NewRouter(handler.Deps{
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Environment: "local",
		Version:     "test",
		Tokens:      tokens,
		Accounts:    store,
		Health:      store,
		SignIn:      service.NewSignIn(store, tokens, map[string]service.Verifier{"google": fakeGoogle{}}),
		Account:     service.NewAccounts(store),
		Patients:    service.NewPatients(store),
		Consents:    service.NewConsents(store),
	})
}

func (c *client) call(method, path, body string) (int, map[string]any) {
	c.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	rec := httptest.NewRecorder()
	c.api.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			c.t.Fatalf("%s %s: %q", method, path, rec.Body.String())
		}
	}
	return rec.Code, out
}

func (c *client) must(want int, method, path, body string) map[string]any {
	c.t.Helper()
	status, out := c.call(method, path, body)
	if status != want {
		c.t.Fatalf("%s %s = %d %v, want %d", method, path, status, out, want)
	}
	return out
}

// signUpPatient signs in with the fake Google and completes the onboarding.
func signUpPatient(t *testing.T, api http.Handler, subject, birthDate string) *client {
	c := &client{t: t, api: api}
	session := c.must(200, "POST", "/v1/auth/google", `{"id_token":"google:`+subject+`"}`)
	c.token = session["access_token"].(string)
	c.must(201, "POST", "/v1/me/patient", `{"birth_date":"`+birthDate+`","diabetes_type":"type_1",
		"consents":[{"consent_type":"terms_of_service","document_version":"1.0"},
		            {"consent_type":"health_data_processing","document_version":"1.0"}]}`)
	return c
}

// DVT-78 "Done when": a patient cannot read another patient's data.
func TestPatientCannotReadAnotherPatientsData(t *testing.T) {
	api := newAPI(t)
	manuel := signUpPatient(t, api, "manuel", "1968-03-02")
	rosa := signUpPatient(t, api, "rosa", "1955-07-19")

	manuel.must(200, "PATCH", "/v1/me/patient", `{"weight_kg": 82.5}`)
	manuel.must(201, "POST", "/v1/me/consents", `{"consent_type":"external_ai_processing","document_version":"1.0"}`)

	// Every patient endpoint answers with the caller's own data: there is no
	// way to name another patient.
	m := manuel.must(200, "GET", "/v1/me/patient", "")
	r := rosa.must(200, "GET", "/v1/me/patient", "")
	if m["id"] == r["id"] || m["birth_date"] != "1968-03-02" || r["birth_date"] != "1955-07-19" || r["weight_kg"] != nil {
		t.Fatalf("manuel = %v\nrosa = %v", m, r)
	}
	if items := rosa.must(200, "GET", "/v1/me/consents", "")["items"].([]any); len(items) != 2 {
		t.Fatalf("rosa sees %d consents, want her own 2", len(items))
	}
	if me := rosa.must(200, "GET", "/v1/me", ""); me["email"] != "rosa@example.com" {
		t.Fatalf("rosa's account = %v", me)
	}

	// A token edited to name Manuel is rejected (bad signature)
	parts := strings.Split(rosa.token, ".")
	forged := &client{t: t, api: api, token: parts[0] + "." + strings.Split(manuel.token, ".")[1] + "." + parts[2]}
	if status, _ := forged.call("GET", "/v1/me/patient", ""); status != 401 {
		t.Fatalf("forged token = %d, want 401", status)
	}
}

func TestAccountLifecycle(t *testing.T) {
	api := newAPI(t)
	manuel := signUpPatient(t, api, "manuel", "1968-03-02")

	me := manuel.must(200, "GET", "/v1/me", "")
	if roles := me["profile_types"].([]any); len(roles) != 1 || roles[0] != "patient" {
		t.Fatalf("profile_types = %v", roles)
	}
	manuel.must(409, "POST", "/v1/me/patient", `{"birth_date":"1968-03-02","diabetes_type":"type_1","consents":[]}`)
	manuel.must(409, "DELETE", "/v1/me/consents/terms_of_service", "")
	manuel.must(404, "DELETE", "/v1/me/consents/beta_testing", "")

	// A second sign-in finds the same account
	again := &client{t: t, api: api}
	if s := again.must(200, "POST", "/v1/auth/google", `{"id_token":"google:manuel"}`); s["user"].(map[string]any)["id"] != me["id"] {
		t.Fatalf("second sign-in created another account: %v", s)
	}

	// Deleting the account: the token stops working and signing in is refused
	manuel.must(204, "DELETE", "/v1/me", "")
	manuel.must(401, "GET", "/v1/me", "")
	status, body := again.call("POST", "/v1/auth/google", `{"id_token":"google:manuel"}`)
	if status != 403 || body["code"] != "account_pending_deletion" {
		t.Fatalf("sign-in after deletion = %d %v", status, body)
	}

	// A token the provider rejects, and a provider this server has not configured
	if status, _ := again.call("POST", "/v1/auth/google", `{"id_token":"not-google"}`); status != 401 {
		t.Fatalf("invalid provider token = %d", status)
	}
	if status, body := again.call("POST", "/v1/auth/microsoft", `{"id_token":"x"}`); status != 503 || body["code"] != "provider_unavailable" {
		t.Fatalf("provider not configured = %d %v", status, body)
	}
}

func TestHealthWithTheDatabase(t *testing.T) {
	c := &client{t: t, api: newAPI(t)}
	if body := c.must(200, "GET", "/v1/health", ""); body["status"] != "ok" {
		t.Fatalf("health = %v", body)
	}
}
