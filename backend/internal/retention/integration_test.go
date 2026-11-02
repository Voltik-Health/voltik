package retention_test

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/database/dbtest"
	"github.com/voltik/voltik/backend/internal/domain"
	"github.com/voltik/voltik/backend/internal/repository"
	"github.com/voltik/voltik/backend/internal/retention"
)

func TestMain(m *testing.M) { dbtest.Main(m) }

// DVT-78 "Done when": deleting an account and running the retention job with
// a date 31 days later erases the account and its health data; the audit log
// keeps its rows without the person.
func TestRetentionErasesDeletedAccounts(t *testing.T) {
	ctx := context.Background()
	store := repository.New(dbtest.API(t))
	meta := domain.RequestMeta{IP: netip.MustParseAddr("192.0.2.10")}

	newPatient := func(subject string) (uuid.UUID, uuid.UUID) {
		a, err := store.CreateAccount(ctx, domain.User{FullName: subject}, domain.Identity{Provider: "google", Subject: subject})
		if err != nil {
			t.Fatal(err)
		}
		docs, _ := store.LegalDocuments(ctx)
		var mandatory []domain.LegalDocument
		for _, d := range docs {
			if d.IsMandatory {
				mandatory = append(mandatory, d)
			}
		}
		p, err := store.CreatePatient(ctx, domain.Patient{UserID: a.ID, BirthDate: time.Date(1968, 3, 2, 0, 0, 0, 0, time.UTC),
			DiabetesType: "type_1", Thresholds: domain.DefaultThresholds}, mandatory, meta)
		if err != nil {
			t.Fatal(err)
		}
		// Health data hanging from the patient: a sensor with a reading
		dbtest.Exec(t, `
			WITH d AS (INSERT INTO devices (patient_id, device_type_id, brand_model)
			           SELECT $1, id, 'Voltik CGM simulator' FROM device_types WHERE code = 'cgm_sensor' RETURNING id)
			INSERT INTO glucose_readings (patient_id, device_id, measured_at, value_mg_dl, source)
			SELECT $1, d.id, now(), 120, 'cgm_sensor' FROM d`, p.ID)
		return a.ID, p.ID
	}

	gone, gonePatient := newPatient("leaves")      // deletes the account today
	recent, _ := newPatient("leaves-later")        // deleted, but the 30 days have not passed
	staying, stayingPatient := newPatient("stays") // keeps the account
	clinician, err := store.CreateAccount(ctx, domain.User{FullName: "Ana"}, domain.Identity{Provider: "microsoft", Subject: "ana"})
	if err != nil {
		t.Fatal(err)
	}
	dbtest.Exec(t, `INSERT INTO health_professionals (user_id, license_number, specialty_id) VALUES ($1, 'LIC-1', 1)`, clinician.ID)

	for _, id := range []uuid.UUID{gone, recent, clinician.ID} {
		if err := store.RequestDeletion(ctx, id, nil, meta); err != nil {
			t.Fatal(err)
		}
	}
	dbtest.Exec(t, `UPDATE users SET deleted_at = now() + interval '10 days' WHERE id = $1`, recent)

	// The job runs 31 days from now
	res, err := retention.NewJob(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(ctx, time.Now().Add(31*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if res.Erased != 1 || res.Failed != 0 || res.RetainedClinicians != 1 || res.Skipped {
		t.Fatalf("result = %+v", res)
	}

	count := func(sql string, args ...any) int { return dbtest.Count(t, sql, args...) }
	for name, got := range map[string]int{
		"user":              count(`SELECT count(*) FROM users WHERE id = $1`, gone),
		"identity":          count(`SELECT count(*) FROM external_identities WHERE user_id = $1`, gone),
		"consents":          count(`SELECT count(*) FROM consents WHERE user_id = $1`, gone),
		"patient":           count(`SELECT count(*) FROM patients WHERE id = $1`, gonePatient),
		"devices":           count(`SELECT count(*) FROM devices WHERE patient_id = $1`, gonePatient),
		"glucose readings":  count(`SELECT count(*) FROM glucose_readings WHERE patient_id = $1`, gonePatient),
		"audit with person": count(`SELECT count(*) FROM data_access_audit_log WHERE actor_user_id = $1 OR target_patient_id = $2`, gone, gonePatient),
	} {
		if got != 0 {
			t.Errorf("%s of the erased account: %d rows left", name, got)
		}
	}
	// 2 consents + the deletion request of the erased account stay, anonymous
	if n := count(`SELECT count(*) FROM data_access_audit_log WHERE actor_user_id IS NULL`); n != 3 {
		t.Errorf("anonymous audit rows = %d, want 3", n)
	}
	for name, got := range map[string]int{
		"recently deleted account": count(`SELECT count(*) FROM users WHERE id = $1`, recent),
		"live account":             count(`SELECT count(*) FROM users WHERE id = $1 AND deleted_at IS NULL`, staying),
		"live readings":            count(`SELECT count(*) FROM glucose_readings WHERE patient_id = $1`, stayingPatient),
		"deleted clinician":        count(`SELECT count(*) FROM users WHERE id = $1`, clinician.ID),
	} {
		if got != 1 {
			t.Errorf("%s: %d rows, want 1", name, got)
		}
	}
}
