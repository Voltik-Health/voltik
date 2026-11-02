package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/auth"
	"github.com/voltik/voltik/backend/internal/domain"
)

// --- fakes -------------------------------------------------------------------

type fakeHealth struct{ err error }

func (f fakeHealth) Ping(context.Context) error { return f.err }

type fakeLoader map[uuid.UUID]domain.Account

func (f fakeLoader) LoadAccount(_ context.Context, id uuid.UUID) (domain.Account, error) {
	if a, ok := f[id]; ok {
		return a, nil
	}
	return domain.Account{}, domain.ErrNotFound
}

type fakeSignIn struct {
	session  domain.Session
	err      error
	provider string
	fullName string
}

func (f *fakeSignIn) SignIn(_ context.Context, provider, _, fullName string) (domain.Session, error) {
	f.provider, f.fullName = provider, fullName
	return f.session, f.err
}

type fakeAccount struct {
	deleted bool
	meta    domain.RequestMeta
}

func (f *fakeAccount) Update(_ context.Context, a domain.Account, change func(*domain.User)) (domain.Account, error) {
	change(&a.User)
	return a, a.ValidateProfile()
}

func (f *fakeAccount) Delete(_ context.Context, _ domain.Account, meta domain.RequestMeta) error {
	f.deleted, f.meta = true, meta
	return nil
}

type fakePatients struct {
	current  domain.Patient
	created  domain.Patient
	consents []domain.ConsentRequest
}

func (f *fakePatients) Get(context.Context, domain.Account) (domain.Patient, error) {
	return f.current, nil
}

func (f *fakePatients) Create(_ context.Context, _ domain.Account, p domain.Patient, c []domain.ConsentRequest, _ domain.RequestMeta) (domain.Patient, error) {
	f.created, f.consents = p, c
	p.ID = uuid.New()
	return p, nil
}

func (f *fakePatients) Update(_ context.Context, _ domain.Account, change func(*domain.Patient)) (domain.Patient, error) {
	change(&f.current)
	return f.current, nil
}

type fakeConsents struct {
	created bool
	revoked string
}

func (f *fakeConsents) List(context.Context, domain.Account) ([]domain.Consent, error) {
	return []domain.Consent{{DocumentType: "terms_of_service", Version: "1.0", CreatedAt: time.Now()}}, nil
}

func (f *fakeConsents) Accept(_ context.Context, _ domain.Account, r domain.ConsentRequest, _ domain.RequestMeta) (domain.Consent, bool, error) {
	return domain.Consent{DocumentType: r.DocumentType, Version: r.Version, CreatedAt: time.Now()}, f.created, nil
}

func (f *fakeConsents) Revoke(_ context.Context, _ domain.Account, docType string, _ domain.RequestMeta) error {
	f.revoked = docType
	return nil
}

// --- test server ---------------------------------------------------------------

type testAPI struct {
	t        *testing.T
	handler  http.Handler
	token    string // session token of the signed-in patient
	account  domain.Account
	health   *fakeHealth
	signIn   *fakeSignIn
	accounts *fakeAccount
	patients *fakePatients
	consents *fakeConsents
}

func newTestAPI(t *testing.T) *testAPI {
	tokens := auth.NewTokens([]byte("0123456789abcdef0123456789abcdef"), "local")
	patientID := uuid.New()
	a := domain.Account{
		User:  domain.User{ID: uuid.New(), FullName: "Manuel Sousa", AccountStatus: domain.AccountActive, CreatedAt: time.Now()},
		Roles: []domain.Role{domain.RolePatient}, PatientID: &patientID,
	}
	token, _, err := tokens.Issue(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	api := &testAPI{t: t, token: token, account: a, health: &fakeHealth{}, signIn: &fakeSignIn{},
		accounts: &fakeAccount{}, patients: &fakePatients{}, consents: &fakeConsents{}}
	api.handler = NewRouter(Deps{
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Environment: "local",
		Version:     "test",
		Tokens:      tokens,
		Accounts:    fakeLoader{a.ID: a},
		Health:      api.health,
		SignIn:      api.signIn,
		Account:     api.accounts,
		Patients:    api.patients,
		Consents:    api.consents,
	})
	return api
}

// do sends a request, with the patient's session token when signedIn, and decodes the JSON answer.
func (a *testAPI) do(method, path, body string, signedIn bool) (*httptest.ResponseRecorder, map[string]any) {
	a.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if signedIn {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			a.t.Fatalf("%s %s: answer is not JSON: %q", method, path, rec.Body.String())
		}
	}
	return rec, out
}

func expectError(t *testing.T, rec *httptest.ResponseRecorder, body map[string]any, status int, code string) {
	t.Helper()
	if rec.Code != status || body["code"] != code {
		t.Fatalf("got %d %v, want %d %s", rec.Code, body, status, code)
	}
	if _, ok := body["details"].([]any); !ok || body["message"] == "" {
		t.Fatalf("error body is not {code, message, details}: %v", body)
	}
}

// --- tests ---------------------------------------------------------------------

func TestHealth(t *testing.T) {
	api := newTestAPI(t)
	rec, body := api.do("GET", "/v1/health", "", false)
	if rec.Code != 200 || body["status"] != "ok" || body["environment"] != "local" || body["version"] != "test" {
		t.Fatalf("health = %d %v", rec.Code, body)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Fatal("no X-Request-ID")
	}

	api.health.err = errors.New("connection refused")
	rec, body = api.do("GET", "/v1/health", "", false)
	expectError(t, rec, body, 503, "service_unavailable")
}

func TestAuthenticationRequired(t *testing.T) {
	api := newTestAPI(t)
	for _, path := range []string{"/v1/me", "/v1/me/patient", "/v1/me/consents", "/v1/unknown"} {
		rec, body := api.do("GET", path, "", false)
		expectError(t, rec, body, 401, "unauthenticated")
		if rec.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("%s: no WWW-Authenticate header", path)
		}
	}
	rec, body := api.do("GET", "/v1/unknown", "", true)
	expectError(t, rec, body, 404, "not_found")
}

func TestSignIn(t *testing.T) {
	api := newTestAPI(t)
	api.signIn.session = domain.Session{Token: "voltik-token", ExpiresAt: time.Date(2026, 11, 9, 9, 30, 0, 0, time.UTC),
		Account: domain.Account{User: domain.User{ID: uuid.New(), FullName: "Voltik user", AccountStatus: "active"}, Roles: []domain.Role{}}}

	rec, body := api.do("POST", "/v1/auth/google", `{"id_token":"google-token"}`, false)
	if rec.Code != 200 || body["access_token"] != "voltik-token" || body["token_type"] != "Bearer" || body["expires_at"] != "2026-11-09T09:30:00Z" {
		t.Fatalf("sign-in = %d %v", rec.Code, body)
	}
	user := body["user"].(map[string]any)
	// Every field is present; no email is null, no profile is [] (contract conventions)
	if v, ok := user["email"]; !ok || v != nil {
		t.Fatalf("email = %v (present: %v)", v, ok)
	}
	if roles, ok := user["profile_types"].([]any); !ok || len(roles) != 0 {
		t.Fatalf("profile_types = %v", user["profile_types"])
	}

	_, _ = api.do("POST", "/v1/auth/apple", `{"id_token":"apple-token","full_name":" Inês Sousa "}`, false)
	if api.signIn.provider != "apple" || api.signIn.fullName != "Inês Sousa" {
		t.Fatalf("apple sign-in got %q %q", api.signIn.provider, api.signIn.fullName)
	}

	rec, body = api.do("POST", "/v1/auth/facebook", `{"access_token":"  "}`, false)
	expectError(t, rec, body, 400, "validation_failed")
	if d := body["details"].([]any)[0].(map[string]any); d["field"] != "access_token" {
		t.Fatalf("details = %v", body["details"])
	}

	rec, body = api.do("POST", "/v1/auth/microsoft", `{"id_token":`, false)
	expectError(t, rec, body, 400, "invalid_request")
}

func TestSignInErrors(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{domain.ErrInvalidProviderToken, 401, "invalid_provider_token"},
		{domain.ErrAccountDisabled, 403, "account_disabled"},
		{&domain.PendingDeletionError{EraseAt: time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)}, 403, "account_pending_deletion"},
		{domain.ErrEmailInUse, 409, "email_in_use"},
		{domain.ErrProviderUnavailable, 503, "provider_unavailable"},
		{domain.ErrServiceUnavailable, 503, "service_unavailable"},
		{errors.New("boom"), 500, "internal_error"},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			api := newTestAPI(t)
			api.signIn.err = tt.err
			rec, body := api.do("POST", "/v1/auth/google", `{"id_token":"t"}`, false)
			expectError(t, rec, body, tt.status, tt.code)
		})
	}
}

func TestErrorResponse(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{&badRequest{message: "x"}, 400, "invalid_request"},
		{domain.Invalid("phone", "must not be blank"), 400, "validation_failed"},
		{domain.ErrUnauthenticated, 401, "unauthenticated"},
		{domain.ErrNotFound, 404, "not_found"},
		{domain.ErrPatientProfileNotFound, 404, "patient_profile_not_found"},
		{domain.ErrPatientProfileExists, 409, "patient_profile_exists"},
		{domain.ErrMandatoryConsent, 409, "mandatory_consent"},
		{&domain.OutdatedDocumentError{DocumentType: "terms_of_service", Version: "1.0"}, 409, "document_outdated"},
		{domain.ErrProfessionalHasPatients, 409, "professional_has_patients"},
	}
	for _, tt := range tests {
		if status, body := errorResponse(tt.err); status != tt.status || body.Code != tt.code || body.Details == nil {
			t.Errorf("%v → %d %+v, want %d %s", tt.err, status, body, tt.status, tt.code)
		}
	}
}

func TestMe(t *testing.T) {
	api := newTestAPI(t)
	rec, body := api.do("GET", "/v1/me", "", true)
	if rec.Code != 200 || body["full_name"] != "Manuel Sousa" || body["profile_types"].([]any)[0] != "patient" {
		t.Fatalf("me = %d %v", rec.Code, body)
	}

	rec, body = api.do("PATCH", "/v1/me", `{"phone":"+351912345678"}`, true)
	if rec.Code != 200 || body["phone"] != "+351912345678" || body["full_name"] != "Manuel Sousa" {
		t.Fatalf("patch = %d %v", rec.Code, body)
	}
	rec, body = api.do("PATCH", "/v1/me", `{"phone":null}`, true)
	if v, ok := body["phone"]; rec.Code != 200 || !ok || v != nil {
		t.Fatalf("clearing the phone = %d %v", rec.Code, body)
	}
	rec, body = api.do("PATCH", "/v1/me", `{}`, true)
	expectError(t, rec, body, 400, "invalid_request")
	rec, body = api.do("PATCH", "/v1/me", `{"full_name":"  "}`, true)
	expectError(t, rec, body, 400, "validation_failed")

	req := httptest.NewRequest("DELETE", "/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+api.token)
	req.RemoteAddr = "172.18.0.5:41000" // the Nginx container
	req.Header.Set("X-Real-IP", "203.0.113.7")
	rec = httptest.NewRecorder()
	api.handler.ServeHTTP(rec, req)
	if rec.Code != 204 || !api.accounts.deleted || api.accounts.meta.IP.String() != "203.0.113.7" {
		t.Fatalf("delete = %d, deleted %v from %v", rec.Code, api.accounts.deleted, api.accounts.meta.IP)
	}
}

func TestCreatePatient(t *testing.T) {
	api := newTestAPI(t)
	rec, body := api.do("POST", "/v1/me/patient", `{
		"birth_date": "1968-03-02", "diabetes_type": "type_1", "current_therapy": "multiple_daily_injections",
		"target_min_mg_dl": 80, "weight_kg": 82.5,
		"consents": [{"consent_type": "terms_of_service", "document_version": "1.0"},
		             {"consent_type": "health_data_processing", "document_version": "1.0"}]}`, true)
	if rec.Code != 201 {
		t.Fatalf("create = %d %v", rec.Code, body)
	}
	p := api.patients.created
	if p.TargetMin != 80 || p.TargetMax != 180 || p.CriticalLow != 55 || p.CriticalHigh != 250 || *p.Therapy != "multiple_daily_injections" {
		t.Fatalf("patient sent to the service = %+v", p)
	}
	if body["birth_date"] != "1968-03-02" || body["gender"] != nil || body["weight_kg"] != 82.5 || len(api.patients.consents) != 2 {
		t.Fatalf("answer = %v", body)
	}

	rec, body = api.do("POST", "/v1/me/patient", `{"diabetes_type": "type_9", "gender": "x",
		"consents": [{"consent_type": "cookies", "document_version": ""}]}`, true)
	expectError(t, rec, body, 400, "validation_failed")
	var fields []string
	for _, d := range body["details"].([]any) {
		fields = append(fields, d.(map[string]any)["field"].(string))
	}
	want := "birth_date,diabetes_type,gender,consents[0].consent_type,consents[0].document_version"
	if strings.Join(fields, ",") != want {
		t.Fatalf("fields = %v, want %s", fields, want)
	}
}

func TestUpdatePatient(t *testing.T) {
	api := newTestAPI(t)
	api.patients.current = domain.Patient{
		BirthDate: time.Date(1968, 3, 2, 0, 0, 0, 0, time.UTC), DiabetesType: "type_1", Thresholds: domain.DefaultThresholds,
		Gender: ptr("male"), Occupation: ptr("Metalworker"), WeightKg: ptr(82.5),
	}
	rec, body := api.do("PATCH", "/v1/me/patient", `{"gender": null, "weight_kg": 81.5, "critical_low_mg_dl": 60}`, true)
	if rec.Code != 200 || body["gender"] != nil || body["occupation"] != "Metalworker" || body["weight_kg"] != 81.5 || body["critical_low_mg_dl"] != 60.0 {
		t.Fatalf("patch = %d %v", rec.Code, body)
	}
	rec, body = api.do("PATCH", "/v1/me/patient", `{}`, true)
	expectError(t, rec, body, 400, "invalid_request")
	rec, body = api.do("PATCH", "/v1/me/patient", `{"current_therapy": "magic"}`, true)
	expectError(t, rec, body, 400, "validation_failed")
}

func TestConsents(t *testing.T) {
	api := newTestAPI(t)
	rec, body := api.do("GET", "/v1/me/consents", "", true)
	if rec.Code != 200 || len(body["items"].([]any)) != 1 {
		t.Fatalf("list = %d %v", rec.Code, body)
	}

	api.consents.created = true
	rec, _ = api.do("POST", "/v1/me/consents", `{"consent_type":"external_ai_processing","document_version":"1.0"}`, true)
	if rec.Code != 201 {
		t.Fatalf("new consent = %d", rec.Code)
	}
	api.consents.created = false
	rec, body = api.do("POST", "/v1/me/consents", `{"consent_type":"external_ai_processing","document_version":"1.0"}`, true)
	if rec.Code != 200 || body["revoked_at"] != nil {
		t.Fatalf("consent already active = %d %v", rec.Code, body)
	}

	rec, _ = api.do("DELETE", "/v1/me/consents/beta_testing", "", true)
	if rec.Code != 204 || api.consents.revoked != "beta_testing" {
		t.Fatalf("revoke = %d, revoked %q", rec.Code, api.consents.revoked)
	}
	rec, body = api.do("DELETE", "/v1/me/consents/cookies", "", true)
	expectError(t, rec, body, 400, "validation_failed")
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		remote, realIP, want string
	}{
		{"203.0.113.7:5000", "", "203.0.113.7"},
		{"203.0.113.7:5000", "198.51.100.1", "203.0.113.7"}, // a client on the internet cannot fake it
		{"172.18.0.5:5000", "198.51.100.1", "198.51.100.1"}, // the proxy can
		{"127.0.0.1:5000", "", "127.0.0.1"},
		{"[::ffff:10.0.0.2]:5000", "2001:db8::1", "2001:db8::1"},
	}
	for _, tt := range tests {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tt.remote
		if tt.realIP != "" {
			r.Header.Set("X-Real-IP", tt.realIP)
		}
		if got := clientIP(r).String(); got != tt.want {
			t.Errorf("clientIP(%s, %q) = %s, want %s", tt.remote, tt.realIP, got, tt.want)
		}
	}
}

func TestRequestIDFromProxy(t *testing.T) {
	api := newTestAPI(t)
	req := httptest.NewRequest("GET", "/v1/health", nil)
	req.Header.Set("X-Request-ID", "abc-123")
	rec := httptest.NewRecorder()
	api.handler.ServeHTTP(rec, req)
	if rec.Header().Get("X-Request-ID") != "abc-123" {
		t.Fatalf("X-Request-ID = %q", rec.Header().Get("X-Request-ID"))
	}
}
