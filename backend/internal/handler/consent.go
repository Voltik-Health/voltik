package handler

import (
	"context"

	"github.com/voltik/voltik/backend/internal/api"
	"github.com/voltik/voltik/backend/internal/domain"
)

func (s *Server) ListConsents(ctx context.Context, _ api.ListConsentsRequestObject) (api.ListConsentsResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	consents, err := s.consents.List(ctx, a)
	if err != nil {
		return nil, err
	}
	items := make([]api.Consent, 0, len(consents))
	for _, c := range consents {
		items = append(items, toConsent(c))
	}
	return api.ListConsents200JSONResponse{Items: items}, nil
}

func (s *Server) AcceptConsent(ctx context.Context, req api.AcceptConsentRequestObject) (api.AcceptConsentResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	var v domain.ValidationError
	consent := consentRequest(&v, "", *req.Body)
	if err := v.OrNil(); err != nil {
		return nil, err
	}
	c, created, err := s.consents.Accept(ctx, a, consent, requestMeta(ctx))
	if err != nil {
		return nil, err
	}
	if created {
		return api.AcceptConsent201JSONResponse(toConsent(c)), nil
	}
	return api.AcceptConsent200JSONResponse(toConsent(c)), nil
}

func (s *Server) RevokeConsent(ctx context.Context, req api.RevokeConsentRequestObject) (api.RevokeConsentResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if !req.ConsentType.Valid() {
		return nil, domain.Invalid("consent_type", "must be one of the listed values")
	}
	if err := s.consents.Revoke(ctx, a, string(req.ConsentType), requestMeta(ctx)); err != nil {
		return nil, err
	}
	return api.RevokeConsent204Response{}, nil
}
