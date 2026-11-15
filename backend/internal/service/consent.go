package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

type ConsentStore interface {
	LegalDocuments(ctx context.Context) ([]domain.LegalDocument, error)
	ListConsents(ctx context.Context, userID uuid.UUID) ([]domain.Consent, error)
	AcceptConsent(ctx context.Context, userID uuid.UUID, patientID *uuid.UUID, doc domain.LegalDocument, meta domain.RequestMeta) (domain.Consent, bool, error)
	ActiveConsents(ctx context.Context, userID uuid.UUID, docType string) ([]domain.LegalDocument, error)
	RevokeConsents(ctx context.Context, userID uuid.UUID, patientID *uuid.UUID, docType string, meta domain.RequestMeta) error
}

// Consents records the GDPR consents (SRS UC012, UC013, UC015).
type Consents struct {
	store ConsentStore
	now   func() time.Time
}

func NewConsents(store ConsentStore) *Consents {
	return &Consents{store: store, now: time.Now}
}

// List returns every consent of the user, newest first, revoked ones included.
func (s *Consents) List(ctx context.Context, a domain.Account) ([]domain.Consent, error) {
	return s.store.ListConsents(ctx, a.ID)
}

// Accept records the acceptance of the version in force of a document.
// created is false when that version was already accepted and active.
func (s *Consents) Accept(ctx context.Context, a domain.Account, req domain.ConsentRequest, meta domain.RequestMeta) (c domain.Consent, created bool, err error) {
	all, err := s.store.LegalDocuments(ctx)
	if err != nil {
		return c, false, err
	}
	issues := &domain.ValidationError{}
	docs, err := resolveConsents(all, []domain.ConsentRequest{req}, s.now(), issues, func(_ int, field string) string { return field })
	if err != nil {
		return c, false, err
	}
	if err := issues.OrNil(); err != nil {
		return c, false, err
	}
	return s.store.AcceptConsent(ctx, a.ID, a.PatientID, docs[0], meta)
}

// Revoke revokes the user's active consent of a document type. Mandatory
// documents cannot be revoked: the app offers to delete the account instead.
func (s *Consents) Revoke(ctx context.Context, a domain.Account, docType string, meta domain.RequestMeta) error {
	active, err := s.store.ActiveConsents(ctx, a.ID, docType)
	if err != nil {
		return err
	}
	if len(active) == 0 {
		return domain.ErrNotFound
	}
	for _, d := range active {
		if d.IsMandatory {
			return domain.ErrMandatoryConsent
		}
	}
	return s.store.RevokeConsents(ctx, a.ID, a.PatientID, docType, meta)
}

// resolveConsents finds the document version each request names. Only the
// version in force can be accepted: an older one is an OutdatedDocumentError,
// an unknown one a validation issue (added to issues, named by field).
func resolveConsents(all []domain.LegalDocument, reqs []domain.ConsentRequest, now time.Time,
	issues *domain.ValidationError, field func(i int, name string) string) ([]domain.LegalDocument, error) {
	inForce := domain.DocumentsInForce(all, now)
	var docs []domain.LegalDocument
	for i, r := range reqs {
		current, ok := inForce[r.DocumentType]
		switch {
		case !ok:
			issues.Add(field(i, "consent_type"), "no version of this document is in force")
		case r.Version == current.Version:
			if !containsType(docs, current.Type) {
				docs = append(docs, current)
			}
		case published(all, r.DocumentType, r.Version):
			return nil, &domain.OutdatedDocumentError{DocumentType: r.DocumentType, Version: r.Version}
		default:
			issues.Add(field(i, "document_version"), "unknown version")
		}
	}
	return docs, nil
}

func published(all []domain.LegalDocument, docType, version string) bool {
	for _, d := range all {
		if d.Type == docType && d.Version == version {
			return true
		}
	}
	return false
}
