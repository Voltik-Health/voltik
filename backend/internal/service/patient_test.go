package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

var testNow = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)

func newPatients(store *fakeStore) *Patients {
	s := NewPatients(store)
	s.now = func() time.Time { return testNow }
	return s
}

func newPatient() domain.Patient {
	return domain.Patient{
		BirthDate:    time.Date(1968, 3, 2, 0, 0, 0, 0, time.UTC),
		DiabetesType: "type_1",
		Thresholds:   domain.DefaultThresholds,
	}
}

var mandatoryConsents = []domain.ConsentRequest{
	{DocumentType: domain.DocTermsOfService, Version: "1.0"},
	{DocumentType: domain.DocHealthDataProcessing, Version: "1.0"},
}

func issueFields(t *testing.T, err error) string {
	t.Helper()
	var v *domain.ValidationError
	if !errors.As(err, &v) {
		t.Fatalf("err = %v, want a validation error", err)
	}
	var f []string
	for _, i := range v.Issues {
		f = append(f, i.Field+": "+i.Issue)
	}
	return strings.Join(f, "; ")
}

func TestPatientsCreate(t *testing.T) {
	store := &fakeStore{docs: publishedDocs(testNow)}
	a := domain.Account{User: domain.User{ID: uuid.New()}}

	p := newPatient()
	p.Occupation = ptr(" Metalworker ")
	created, err := newPatients(store).Create(context.Background(), a, p, mandatoryConsents, domain.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if created.UserID != a.ID || *created.Occupation != "Metalworker" || len(store.createdDocs) != 2 {
		t.Fatalf("created %+v with %d consents", created, len(store.createdDocs))
	}
}

func TestPatientsCreateRejects(t *testing.T) {
	existing := uuid.New()
	tests := []struct {
		name     string
		account  domain.Account
		change   func(*domain.Patient)
		consents []domain.ConsentRequest
		want     string // validation issues, or the error
	}{
		{"already onboarded", domain.Account{PatientID: &existing}, nil, mandatoryConsents, domain.ErrPatientProfileExists.Error()},
		{"missing a mandatory consent", domain.Account{}, nil, mandatoryConsents[:1],
			"consents: must include health_data_processing"},
		{"no consents", domain.Account{}, nil, nil,
			"consents: must include health_data_processing; consents: must include terms_of_service"},
		{"unknown version", domain.Account{}, nil,
			[]domain.ConsentRequest{mandatoryConsents[0], {DocumentType: domain.DocHealthDataProcessing, Version: "9.9"}},
			"consents[1].document_version: unknown version; consents: must include health_data_processing"},
		{"profile and consents both wrong", domain.Account{}, func(p *domain.Patient) { p.WeightKg = ptr(1.0) }, mandatoryConsents[1:],
			"weight_kg: must be between 2 and 500; consents: must include terms_of_service"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{docs: publishedDocs(testNow)}
			p := newPatient()
			if tt.change != nil {
				tt.change(&p)
			}
			_, err := newPatients(store).Create(context.Background(), tt.account, p, tt.consents, domain.RequestMeta{})
			var got string
			var v *domain.ValidationError
			if errors.As(err, &v) {
				got = issueFields(t, err)
			} else if err != nil {
				got = err.Error()
			}
			if got != tt.want {
				t.Fatalf("got  %q\nwant %q", got, tt.want)
			}
			if store.createdPatient != nil {
				t.Fatal("the profile was saved")
			}
		})
	}
}

func TestPatientsCreateOutdatedConsent(t *testing.T) {
	docs := append(publishedDocs(testNow), domain.LegalDocument{
		ID: uuid.New(), Type: domain.DocTermsOfService, Version: "1.1", IsMandatory: true, PublishedAt: testNow.AddDate(0, 0, -1),
	})
	store := &fakeStore{docs: docs}
	_, err := newPatients(store).Create(context.Background(), domain.Account{}, newPatient(), mandatoryConsents, domain.RequestMeta{})
	var outdated *domain.OutdatedDocumentError
	if !errors.As(err, &outdated) || outdated.DocumentType != domain.DocTermsOfService || outdated.Version != "1.0" {
		t.Fatalf("err = %v, want version 1.0 of terms_of_service outdated", err)
	}
}

func TestPatientsUpdate(t *testing.T) {
	id := uuid.New()
	a := domain.Account{PatientID: &id}
	current := newPatient()
	store := &fakeStore{patient: &current}

	got, err := newPatients(store).Update(context.Background(), a, func(p *domain.Patient) { p.TargetMin = 80 })
	if err != nil || got.TargetMin != 80 {
		t.Fatalf("update: %+v, %v", got, err)
	}

	_, err = newPatients(store).Update(context.Background(), a, func(p *domain.Patient) { p.TargetMin = 200 })
	if got := issueFields(t, err); got != "target_min_mg_dl: must be lower than target_max_mg_dl; target_max_mg_dl: must be higher than target_min_mg_dl" {
		t.Fatalf("issues = %s", got)
	}
	if store.patient.TargetMin != 80 {
		t.Fatal("an invalid change was saved")
	}

	if _, err := newPatients(store).Update(context.Background(), domain.Account{}, func(*domain.Patient) {}); !errors.Is(err, domain.ErrPatientProfileNotFound) {
		t.Fatalf("without a profile: err = %v", err)
	}
	if _, err := newPatients(store).Get(context.Background(), domain.Account{}); !errors.Is(err, domain.ErrPatientProfileNotFound) {
		t.Fatalf("get without a profile: err = %v", err)
	}
}
