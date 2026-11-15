package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

// fakeStore keeps in memory what the services read and records what they write.
type fakeStore struct {
	inactiveProviders map[string]bool
	accounts          map[string]domain.Account // by provider + "/" + subject
	createErr         error                     // returned by CreateAccount
	racer             *domain.Account           // created by "another request" just before CreateAccount
	signIns           int
	created           []domain.User

	user           domain.User
	followUps      bool
	deletionCalled bool
	deletedPatient *uuid.UUID

	docs           []domain.LegalDocument
	patient        *domain.Patient
	createdPatient *domain.Patient
	createdDocs    []domain.LegalDocument
	active         []domain.LegalDocument
	revoked        string
	acceptedDoc    *domain.LegalDocument
}

func (f *fakeStore) ProviderActive(_ context.Context, provider string) (bool, error) {
	return !f.inactiveProviders[provider], nil
}

func (f *fakeStore) FindAccountByIdentity(_ context.Context, provider, subject string) (domain.Account, error) {
	a, ok := f.accounts[provider+"/"+subject]
	if !ok {
		return domain.Account{}, domain.ErrNotFound
	}
	return a, nil
}

func (f *fakeStore) RecordSignIn(context.Context, domain.Identity) error {
	f.signIns++
	return nil
}

func (f *fakeStore) CreateAccount(_ context.Context, u domain.User, id domain.Identity) (domain.Account, error) {
	if f.racer != nil {
		f.accounts = map[string]domain.Account{id.Provider + "/" + id.Subject: *f.racer}
		f.racer = nil
		return domain.Account{}, domain.ErrIdentityTaken
	}
	if f.createErr != nil {
		return domain.Account{}, f.createErr
	}
	f.created = append(f.created, u)
	u.ID = uuid.New()
	u.AccountStatus = domain.AccountActive
	return domain.Account{User: u, Roles: []domain.Role{}}, nil
}

func (f *fakeStore) UpdateUser(_ context.Context, _ uuid.UUID, apply func(*domain.User) error) (domain.User, error) {
	u := f.user
	if err := apply(&u); err != nil {
		return domain.User{}, err
	}
	f.user = u
	return u, nil
}

func (f *fakeStore) HasActiveFollowUps(context.Context, uuid.UUID) (bool, error) {
	return f.followUps, nil
}

func (f *fakeStore) RequestDeletion(_ context.Context, _ uuid.UUID, patientID *uuid.UUID, _ domain.RequestMeta) error {
	f.deletionCalled = true
	f.deletedPatient = patientID
	return nil
}

func (f *fakeStore) GetPatient(context.Context, uuid.UUID) (domain.Patient, error) {
	if f.patient == nil {
		return domain.Patient{}, domain.ErrPatientProfileNotFound
	}
	return *f.patient, nil
}

func (f *fakeStore) CreatePatient(_ context.Context, p domain.Patient, docs []domain.LegalDocument, _ domain.RequestMeta) (domain.Patient, error) {
	f.createdPatient = &p
	f.createdDocs = docs
	return p, nil
}

func (f *fakeStore) UpdatePatient(_ context.Context, _ uuid.UUID, apply func(*domain.Patient) error) (domain.Patient, error) {
	p := *f.patient
	if err := apply(&p); err != nil {
		return domain.Patient{}, err
	}
	f.patient = &p
	return p, nil
}

func (f *fakeStore) LegalDocuments(context.Context) ([]domain.LegalDocument, error) {
	return f.docs, nil
}

func (f *fakeStore) ListConsents(context.Context, uuid.UUID) ([]domain.Consent, error) {
	return nil, nil
}

func (f *fakeStore) AcceptConsent(_ context.Context, _ uuid.UUID, _ *uuid.UUID, doc domain.LegalDocument, _ domain.RequestMeta) (domain.Consent, bool, error) {
	f.acceptedDoc = &doc
	return domain.Consent{DocumentType: doc.Type, Version: doc.Version}, true, nil
}

func (f *fakeStore) ActiveConsents(context.Context, uuid.UUID, string) ([]domain.LegalDocument, error) {
	return f.active, nil
}

func (f *fakeStore) RevokeConsents(_ context.Context, _ uuid.UUID, _ *uuid.UUID, docType string, _ domain.RequestMeta) error {
	f.revoked = docType
	return nil
}

// The legal documents migration 000002 publishes: version 1.0 of each.
func publishedDocs(now time.Time) []domain.LegalDocument {
	at := now.AddDate(0, -1, 0)
	return []domain.LegalDocument{
		{ID: uuid.New(), Type: domain.DocTermsOfService, Version: "1.0", IsMandatory: true, PublishedAt: at},
		{ID: uuid.New(), Type: domain.DocHealthDataProcessing, Version: "1.0", IsMandatory: true, PublishedAt: at},
		{ID: uuid.New(), Type: domain.DocExternalAIProcessing, Version: "1.0", PublishedAt: at},
		{ID: uuid.New(), Type: domain.DocBetaTesting, Version: "1.0", PublishedAt: at},
	}
}

func ptr[T any](v T) *T { return &v }
