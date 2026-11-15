package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

func newConsents(store *fakeStore) *Consents {
	s := NewConsents(store)
	s.now = func() time.Time { return testNow }
	return s
}

func TestConsentsAccept(t *testing.T) {
	docs := publishedDocs(testNow)
	store := &fakeStore{docs: docs}
	c, created, err := newConsents(store).Accept(context.Background(), domain.Account{},
		domain.ConsentRequest{DocumentType: domain.DocExternalAIProcessing, Version: "1.0"}, domain.RequestMeta{})
	if err != nil || !created || c.DocumentType != domain.DocExternalAIProcessing {
		t.Fatalf("accept: %+v, %v, %v", c, created, err)
	}
	if store.acceptedDoc.ID != docs[2].ID {
		t.Fatalf("accepted %+v, want the version in force", store.acceptedDoc)
	}
}

func TestConsentsAcceptRejects(t *testing.T) {
	docs := append(publishedDocs(testNow), domain.LegalDocument{
		ID: uuid.New(), Type: domain.DocBetaTesting, Version: "2.0", PublishedAt: testNow.AddDate(0, 0, -1),
	})
	withoutBeta := publishedDocs(testNow)[:3]

	tests := []struct {
		name string
		docs []domain.LegalDocument
		req  domain.ConsentRequest
		want string
	}{
		{"older version", docs, domain.ConsentRequest{DocumentType: domain.DocBetaTesting, Version: "1.0"}, "outdated"},
		{"unknown version", docs, domain.ConsentRequest{DocumentType: domain.DocBetaTesting, Version: "7.0"}, "document_version"},
		{"nothing in force", withoutBeta, domain.ConsentRequest{DocumentType: domain.DocBetaTesting, Version: "1.0"}, "consent_type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{docs: tt.docs}
			_, _, err := newConsents(store).Accept(context.Background(), domain.Account{}, tt.req, domain.RequestMeta{})
			var outdated *domain.OutdatedDocumentError
			var invalid *domain.ValidationError
			switch {
			case tt.want == "outdated" && errors.As(err, &outdated):
			case errors.As(err, &invalid) && invalid.Issues[0].Field == tt.want:
			default:
				t.Fatalf("err = %v, want %s", err, tt.want)
			}
			if store.acceptedDoc != nil {
				t.Fatal("the consent was saved")
			}
		})
	}
}

func TestConsentsRevoke(t *testing.T) {
	optional := domain.LegalDocument{Type: domain.DocExternalAIProcessing}
	mandatory := domain.LegalDocument{Type: domain.DocTermsOfService, IsMandatory: true}

	store := &fakeStore{active: []domain.LegalDocument{optional}}
	if err := newConsents(store).Revoke(context.Background(), domain.Account{}, optional.Type, domain.RequestMeta{}); err != nil || store.revoked != optional.Type {
		t.Fatalf("optional: err = %v, revoked = %q", err, store.revoked)
	}

	store = &fakeStore{active: []domain.LegalDocument{mandatory}}
	if err := newConsents(store).Revoke(context.Background(), domain.Account{}, mandatory.Type, domain.RequestMeta{}); !errors.Is(err, domain.ErrMandatoryConsent) || store.revoked != "" {
		t.Fatalf("mandatory: err = %v, revoked = %q", err, store.revoked)
	}

	store = &fakeStore{}
	if err := newConsents(store).Revoke(context.Background(), domain.Account{}, optional.Type, domain.RequestMeta{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("none active: err = %v", err)
	}
}
