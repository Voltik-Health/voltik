package handler

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/voltik/voltik/backend/internal/api"
	"github.com/voltik/voltik/backend/internal/domain"
)

func (s *Server) SignInWithGoogle(ctx context.Context, req api.SignInWithGoogleRequestObject) (api.SignInWithGoogleResponseObject, error) {
	session, err := s.startSession(ctx, domain.ProviderGoogle, "id_token", req.Body.IdToken, nil)
	if err != nil {
		return nil, err
	}
	return api.SignInWithGoogle200JSONResponse{SignedInJSONResponse: api.SignedInJSONResponse(toSession(session))}, nil
}

func (s *Server) SignInWithMicrosoft(ctx context.Context, req api.SignInWithMicrosoftRequestObject) (api.SignInWithMicrosoftResponseObject, error) {
	session, err := s.startSession(ctx, domain.ProviderMicrosoft, "id_token", req.Body.IdToken, nil)
	if err != nil {
		return nil, err
	}
	return api.SignInWithMicrosoft200JSONResponse{SignedInJSONResponse: api.SignedInJSONResponse(toSession(session))}, nil
}

func (s *Server) SignInWithApple(ctx context.Context, req api.SignInWithAppleRequestObject) (api.SignInWithAppleResponseObject, error) {
	session, err := s.startSession(ctx, domain.ProviderApple, "id_token", req.Body.IdToken, req.Body.FullName)
	if err != nil {
		return nil, err
	}
	return api.SignInWithApple200JSONResponse{SignedInJSONResponse: api.SignedInJSONResponse(toSession(session))}, nil
}

func (s *Server) SignInWithFacebook(ctx context.Context, req api.SignInWithFacebookRequestObject) (api.SignInWithFacebookResponseObject, error) {
	session, err := s.startSession(ctx, domain.ProviderFacebook, "access_token", req.Body.AccessToken, nil)
	if err != nil {
		return nil, err
	}
	return api.SignInWithFacebook200JSONResponse{SignedInJSONResponse: api.SignedInJSONResponse(toSession(session))}, nil
}

// startSession checks the request fields shared by the four sign-ins and signs in.
func (s *Server) startSession(ctx context.Context, provider, tokenField, token string, fullName *string) (domain.Session, error) {
	var v domain.ValidationError
	if strings.TrimSpace(token) == "" {
		v.Add(tokenField, "is required")
	}
	name := ""
	if fullName != nil {
		name = strings.TrimSpace(*fullName)
		if name == "" || utf8.RuneCountInString(name) > 150 {
			v.Add("full_name", "must have between 1 and 150 characters")
		}
	}
	if err := v.OrNil(); err != nil {
		return domain.Session{}, err
	}
	return s.signIn.SignIn(ctx, provider, strings.TrimSpace(token), name)
}
