package domain

import (
	"time"

	"github.com/google/uuid"
)

// Types of legal document, as in legal_documents.document_type.
const (
	DocTermsOfService       = "terms_of_service"
	DocHealthDataProcessing = "health_data_processing"
	DocExternalAIProcessing = "external_ai_processing"
	DocBetaTesting          = "beta_testing"
)

// LegalDocument is one published version of a legal document.
// A published version is never edited: a change is a new row.
type LegalDocument struct {
	ID          uuid.UUID
	Type        string
	Version     string
	IsMandatory bool
	PublishedAt time.Time
}

// Consent is the user's acceptance of one document version.
type Consent struct {
	DocumentType string
	Version      string
	CreatedAt    time.Time
	RevokedAt    *time.Time
}

// ConsentRequest is a document version the user says they accept.
type ConsentRequest struct {
	DocumentType string
	Version      string
}

// DocumentsInForce returns, for each document type, the version in force at
// now: the one with the latest published_at that is not in the future.
func DocumentsInForce(docs []LegalDocument, now time.Time) map[string]LegalDocument {
	inForce := map[string]LegalDocument{}
	for _, d := range docs {
		if d.PublishedAt.After(now) {
			continue
		}
		if current, ok := inForce[d.Type]; !ok || d.PublishedAt.After(current.PublishedAt) {
			inForce[d.Type] = d
		}
	}
	return inForce
}
