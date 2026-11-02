package repository_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/database/dbtest"
	"github.com/voltik/voltik/backend/internal/domain"
	"github.com/voltik/voltik/backend/internal/repository"
)

// Integration tests: they run against PostgreSQL as voltik_api (see dbtest).
func TestMain(m *testing.M) { dbtest.Main(m) }

var (
	ctx  = context.Background()
	meta = domain.RequestMeta{IP: netip.MustParseAddr("192.0.2.10"), UserAgent: "voltik-tests"}
)

func ptr[T any](v T) *T { return &v }

func newStore(t *testing.T) *repository.Store {
	return repository.New(dbtest.API(t))
}

func createAccount(t *testing.T, s *repository.Store, provider, subject string, email *string) domain.Account {
	t.Helper()
	a, err := s.CreateAccount(ctx, domain.User{Email: email, FullName: "Test " + subject},
		domain.Identity{Provider: provider, Subject: subject, Email: email})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// mandatoryDocs are version 1.0 of the two mandatory documents, published by migration 000002.
func mandatoryDocs(t *testing.T, s *repository.Store) []domain.LegalDocument {
	t.Helper()
	all, err := s.LegalDocuments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out []domain.LegalDocument
	for _, d := range all {
		if d.IsMandatory && d.Version == "1.0" {
			out = append(out, d)
		}
	}
	if len(out) != 2 {
		t.Fatalf("mandatory documents = %+v", out)
	}
	return out
}

func createPatient(t *testing.T, s *repository.Store, userID uuid.UUID) domain.Patient {
	t.Helper()
	p, err := s.CreatePatient(ctx, domain.Patient{
		UserID: userID, BirthDate: time.Date(1968, 3, 2, 0, 0, 0, 0, time.UTC),
		Gender: ptr("male"), Occupation: ptr("Metalworker"), DiabetesType: "type_1", Therapy: ptr("insulin_pump"),
		Thresholds: domain.DefaultThresholds, WeightKg: ptr(82.5), HeightCm: ptr(174),
	}, mandatoryDocs(t, s), meta)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCreateAndFindAccount(t *testing.T) {
	s := newStore(t)
	a := createAccount(t, s, "google", "g-1", ptr("Manuel.Sousa@example.com"))

	found, err := s.FindAccountByIdentity(ctx, "google", "g-1")
	if err != nil || found.ID != a.ID || found.AccountStatus != domain.AccountActive || len(found.Roles) != 0 || found.DeletedAt != nil {
		t.Fatalf("found %+v, %v", found, err)
	}
	if _, err := s.FindAccountByIdentity(ctx, "microsoft", "g-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("other provider: err = %v", err)
	}

	// One email, one account: case does not matter
	_, err = s.CreateAccount(ctx, domain.User{Email: ptr("manuel.sousa@EXAMPLE.com"), FullName: "Other"},
		domain.Identity{Provider: "microsoft", Subject: "m-1"})
	if !errors.Is(err, domain.ErrEmailInUse) {
		t.Fatalf("same email: err = %v", err)
	}
	_, err = s.CreateAccount(ctx, domain.User{FullName: "Other"}, domain.Identity{Provider: "google", Subject: "g-1"})
	if !errors.Is(err, domain.ErrIdentityTaken) {
		t.Fatalf("same identity: err = %v", err)
	}
	if n := dbtest.Count(t, `SELECT count(*) FROM users`); n != 1 {
		t.Fatalf("users = %d: a failed sign-up left rows behind", n)
	}

	// Accounts without email are allowed
	createAccount(t, s, "facebook", "f-1", nil)
	createAccount(t, s, "facebook", "f-2", nil)

	if err := s.RecordSignIn(ctx, domain.Identity{Provider: "google", Subject: "g-1", Email: ptr("new@example.com")}); err != nil {
		t.Fatal(err)
	}
	if n := dbtest.Count(t, `SELECT count(*) FROM external_identities WHERE provider_email = 'new@example.com' AND last_login_at IS NOT NULL`); n != 1 {
		t.Fatal("sign-in not recorded")
	}
}

func TestProviderActive(t *testing.T) {
	s := newStore(t)
	for provider, want := range map[string]bool{"google": true, "microsoft": true, "facebook": true, "apple": false, "github": false} {
		if got, err := s.ProviderActive(ctx, provider); err != nil || got != want {
			t.Errorf("ProviderActive(%s) = %v, %v", provider, got, err)
		}
	}
}

func TestRolesComeFromTheData(t *testing.T) {
	s := newStore(t)
	manuel := createAccount(t, s, "google", "manuel", nil)
	ines := createAccount(t, s, "google", "ines", nil)
	p := createPatient(t, s, manuel.ID)

	dbtest.Exec(t, `INSERT INTO caregiver_links (patient_id, caregiver_user_id, status, linked_at) VALUES ($1, $2, 'active', now())`, p.ID, ines.ID)
	dbtest.Exec(t, `INSERT INTO health_professionals (user_id, license_number, specialty_id) VALUES ($1, 'LIC-1', 1)`, ines.ID)
	dbtest.Exec(t, `INSERT INTO organizations (id, company_name, tax_id, contract_start_date, contract_end_date)
	                VALUES ('10000000-0000-4000-8000-000000000001', 'Test', '500000001', '2026-01-01', '2026-12-31')`)
	dbtest.Exec(t, `INSERT INTO organization_managers (organization_id, user_id) VALUES ('10000000-0000-4000-8000-000000000001', $1)`, ines.ID)
	dbtest.Exec(t, `UPDATE users SET is_platform_admin = TRUE WHERE id = $1`, ines.ID)

	m, err := s.LoadAccount(ctx, manuel.ID)
	if err != nil || len(m.Roles) != 1 || m.Roles[0] != domain.RolePatient || *m.PatientID != p.ID {
		t.Fatalf("manuel = %+v, %v", m, err)
	}
	i, err := s.LoadAccount(ctx, ines.ID)
	want := []domain.Role{domain.RoleCaregiver, domain.RoleHealthProfessional, domain.RoleCompanyManager, domain.RoleAdmin}
	if err != nil || len(i.Roles) != len(want) || i.PatientID != nil {
		t.Fatalf("ines = %+v, %v", i.Roles, err)
	}
	for k := range want {
		if i.Roles[k] != want[k] {
			t.Fatalf("ines roles = %v, want %v", i.Roles, want)
		}
	}

	// A revoked link, a deleted company and a deleted clinician profile give no role
	dbtest.Exec(t, `UPDATE caregiver_links SET status = 'revoked', revoked_at = now()`)
	dbtest.Exec(t, `UPDATE organizations SET deleted_at = now()`)
	dbtest.Exec(t, `UPDATE health_professionals SET deleted_at = now()`)
	i, _ = s.LoadAccount(ctx, ines.ID)
	if len(i.Roles) != 1 || i.Roles[0] != domain.RoleAdmin {
		t.Fatalf("ines roles after the changes = %v", i.Roles)
	}
}

func TestPatientProfile(t *testing.T) {
	s := newStore(t)
	a := createAccount(t, s, "google", "manuel", nil)
	p := createPatient(t, s, a.ID)

	got, err := s.GetPatient(ctx, a.ID)
	if err != nil || got.ID != p.ID || *got.Gender != "male" || got.DiabetesType != "type_1" || *got.Therapy != "insulin_pump" ||
		*got.WeightKg != 82.5 || *got.HeightCm != 174 || !got.BirthDate.Equal(p.BirthDate) || got.TargetMin != 70 {
		t.Fatalf("patient = %+v, %v", got, err)
	}
	if n := dbtest.Count(t, `SELECT count(*) FROM consents WHERE user_id = $1 AND ip_address = '192.0.2.10'`, a.ID); n != 2 {
		t.Fatalf("consents = %d", n)
	}
	if n := dbtest.Count(t, `SELECT count(*) FROM data_access_audit_log WHERE operation = 'consent_granted' AND target_patient_id = $1`, p.ID); n != 2 {
		t.Fatalf("audit rows = %d", n)
	}

	if _, err := s.CreatePatient(ctx, domain.Patient{UserID: a.ID, BirthDate: p.BirthDate, DiabetesType: "type_2",
		Thresholds: domain.DefaultThresholds}, nil, meta); !errors.Is(err, domain.ErrPatientProfileExists) {
		t.Fatalf("second profile: err = %v", err)
	}

	updated, err := s.UpdatePatient(ctx, a.ID, func(p *domain.Patient) error {
		p.Gender, p.Therapy, p.TargetMin = nil, ptr("diet_only"), 80
		return nil
	})
	if err != nil || updated.Gender != nil || *updated.Therapy != "diet_only" || !updated.UpdatedAt.After(p.UpdatedAt) {
		t.Fatalf("updated = %+v, %v", updated, err)
	}

	var invalid *domain.ValidationError
	_, err = s.UpdatePatient(ctx, a.ID, func(p *domain.Patient) error { p.DiabetesType = "type_9"; return nil })
	if !errors.As(err, &invalid) || invalid.Issues[0].Field != "diabetes_type" {
		t.Fatalf("unknown code: err = %v", err)
	}
	if got, _ := s.GetPatient(ctx, a.ID); got.DiabetesType != "type_1" {
		t.Fatal("a rejected change was saved")
	}
}

func TestConsents(t *testing.T) {
	s := newStore(t)
	a := createAccount(t, s, "google", "manuel", nil)
	all, _ := s.LegalDocuments(ctx)
	var ai domain.LegalDocument
	for _, d := range all {
		if d.Type == domain.DocExternalAIProcessing {
			ai = d
		}
	}
	audit := func(op string) int {
		return dbtest.Count(t, `SELECT count(*) FROM data_access_audit_log WHERE operation = $1 AND actor_user_id = $2`, op, a.ID)
	}

	c, created, err := s.AcceptConsent(ctx, a.ID, nil, ai, meta)
	if err != nil || !created || c.Version != "1.0" || c.RevokedAt != nil {
		t.Fatalf("accept: %+v, %v, %v", c, created, err)
	}
	if _, created, _ = s.AcceptConsent(ctx, a.ID, nil, ai, meta); created || audit("consent_granted") != 1 {
		t.Fatalf("accepting again changed something (audit rows %d)", audit("consent_granted"))
	}

	if err := s.RevokeConsents(ctx, a.ID, nil, ai.Type, meta); err != nil || audit("consent_revoked") != 1 {
		t.Fatalf("revoke: %v", err)
	}
	if active, _ := s.ActiveConsents(ctx, a.ID, ai.Type); len(active) != 0 {
		t.Fatalf("still active: %+v", active)
	}
	if err := s.RevokeConsents(ctx, a.ID, nil, ai.Type, meta); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoking twice: err = %v", err)
	}

	// Accepting a revoked version activates it again
	c, created, err = s.AcceptConsent(ctx, a.ID, nil, ai, meta)
	if err != nil || !created || c.RevokedAt != nil || audit("consent_granted") != 2 {
		t.Fatalf("accept again: %+v, %v, %v", c, created, err)
	}
	list, err := s.ListConsents(ctx, a.ID)
	if err != nil || len(list) != 1 || list[0].DocumentType != ai.Type {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestRequestDeletionHidesTheAccount(t *testing.T) {
	s := newStore(t)
	a := createAccount(t, s, "google", "manuel", nil)
	p := createPatient(t, s, a.ID)
	dbtest.Exec(t, `INSERT INTO notification_tokens (user_id, platform, push_token) VALUES ($1, 'android', 'fcm-1')`, a.ID)
	dbtest.Exec(t, `INSERT INTO b2c_subscriptions (payer_user_id, plan_id) SELECT $1, id FROM subscription_plans WHERE code = 'premium_individual'`, a.ID)

	if err := s.RequestDeletion(ctx, a.ID, &p.ID, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadAccount(ctx, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted account still loads: %v", err)
	}
	if _, err := s.GetPatient(ctx, a.ID); !errors.Is(err, domain.ErrPatientProfileNotFound) {
		t.Fatalf("deleted patient still visible: %v", err)
	}
	if _, err := s.UpdateUser(ctx, a.ID, func(*domain.User) error { return nil }); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted user still editable: %v", err)
	}
	found, err := s.FindAccountByIdentity(ctx, "google", "manuel")
	if err != nil || found.DeletedAt == nil || found.AccountStatus != domain.AccountDeactivated || found.PatientID != nil {
		t.Fatalf("sign-in lookup = %+v, %v", found, err)
	}
	for sql, want := range map[string]int{
		`SELECT count(*) FROM notification_tokens WHERE is_active`:                                  0,
		`SELECT count(*) FROM b2c_subscriptions WHERE auto_renew`:                                   0,
		`SELECT count(*) FROM data_access_audit_log WHERE operation = 'account_deletion_requested'`: 1,
	} {
		if got := dbtest.Count(t, sql); got != want {
			t.Errorf("%s = %d, want %d", sql, got, want)
		}
	}
	if err := s.RequestDeletion(ctx, a.ID, &p.ID, meta); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleting twice: err = %v", err)
	}

	// The email is free again: another provider can create a new account with it
	email := ptr("manuel@example.com")
	dbtest.Exec(t, `UPDATE users SET email = $2 WHERE id = $1`, a.ID, *email)
	createAccount(t, s, "microsoft", "manuel", email)
}

func TestHasActiveFollowUps(t *testing.T) {
	s := newStore(t)
	ana := createAccount(t, s, "microsoft", "ana", nil)
	manuel := createAccount(t, s, "google", "manuel", nil)
	p := createPatient(t, s, manuel.ID)
	dbtest.Exec(t, `INSERT INTO health_professionals (id, user_id, license_number, specialty_id)
	                VALUES ('40000000-0000-4000-8000-000000000001', $1, 'LIC-1', 1)`, ana.ID)
	if busy, err := s.HasActiveFollowUps(ctx, ana.ID); err != nil || busy {
		t.Fatalf("no follow-ups yet: %v, %v", busy, err)
	}
	dbtest.Exec(t, `INSERT INTO patient_professional_assignments (patient_id, professional_id)
	                VALUES ($1, '40000000-0000-4000-8000-000000000001')`, p.ID)
	if busy, err := s.HasActiveFollowUps(ctx, ana.ID); err != nil || !busy {
		t.Fatalf("with a follow-up: %v, %v", busy, err)
	}
}
