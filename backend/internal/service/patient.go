package service

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

type PatientStore interface {
	GetPatient(ctx context.Context, userID uuid.UUID) (domain.Patient, error)
	CreatePatient(ctx context.Context, p domain.Patient, docs []domain.LegalDocument, meta domain.RequestMeta) (domain.Patient, error)
	UpdatePatient(ctx context.Context, userID uuid.UUID, apply func(*domain.Patient) error) (domain.Patient, error)
	LegalDocuments(ctx context.Context) ([]domain.LegalDocument, error)
}

// Patients manages the signed-in user's patient profile.
type Patients struct {
	store PatientStore
	now   func() time.Time
}

func NewPatients(store PatientStore) *Patients {
	return &Patients{store: store, now: time.Now}
}

// Get returns the user's patient profile.
func (s *Patients) Get(ctx context.Context, a domain.Account) (domain.Patient, error) {
	if a.PatientID == nil {
		return domain.Patient{}, domain.ErrPatientProfileNotFound
	}
	return s.store.GetPatient(ctx, a.ID)
}

// Create completes the onboarding (SRS UC046): the patient profile plus the
// mandatory consents, each in the version in force, in one transaction.
func (s *Patients) Create(ctx context.Context, a domain.Account, p domain.Patient, consents []domain.ConsentRequest, meta domain.RequestMeta) (domain.Patient, error) {
	if a.PatientID != nil {
		return domain.Patient{}, domain.ErrPatientProfileExists
	}
	p.UserID = a.ID
	p.Occupation = trimmed(p.Occupation)

	now := s.now()
	issues := &domain.ValidationError{}
	var patientIssues *domain.ValidationError
	if errors.As(p.Validate(domain.Today(now)), &patientIssues) {
		issues.Issues = append(issues.Issues, patientIssues.Issues...)
	}
	all, err := s.store.LegalDocuments(ctx)
	if err != nil {
		return domain.Patient{}, err
	}
	docs, err := resolveConsents(all, consents, now, issues, func(i int, field string) string {
		return "consents[" + strconv.Itoa(i) + "]." + field
	})
	if err != nil {
		return domain.Patient{}, err
	}
	inForce := domain.DocumentsInForce(all, now)
	for _, docType := range slices.Sorted(maps.Keys(inForce)) {
		if inForce[docType].IsMandatory && !containsType(docs, docType) {
			issues.Add("consents", "must include "+docType)
		}
	}
	if err := issues.OrNil(); err != nil {
		return domain.Patient{}, err
	}
	return s.store.CreatePatient(ctx, p, docs, meta)
}

// Update applies change to the profile and saves it if the result is valid.
func (s *Patients) Update(ctx context.Context, a domain.Account, change func(*domain.Patient)) (domain.Patient, error) {
	if a.PatientID == nil {
		return domain.Patient{}, domain.ErrPatientProfileNotFound
	}
	today := domain.Today(s.now())
	return s.store.UpdatePatient(ctx, a.ID, func(p *domain.Patient) error {
		change(p)
		p.Occupation = trimmed(p.Occupation)
		return p.Validate(today)
	})
}

func containsType(docs []domain.LegalDocument, docType string) bool {
	for _, d := range docs {
		if d.Type == docType {
			return true
		}
	}
	return false
}
