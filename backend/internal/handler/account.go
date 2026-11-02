package handler

import (
	"context"

	"github.com/voltik/voltik/backend/internal/api"
	"github.com/voltik/voltik/backend/internal/domain"
)

func (s *Server) GetMe(ctx context.Context, _ api.GetMeRequestObject) (api.GetMeResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	return api.GetMe200JSONResponse(toMe(a)), nil
}

func (s *Server) UpdateMe(ctx context.Context, req api.UpdateMeRequestObject) (api.UpdateMeResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	body := req.Body
	if body.FullName == nil && !body.Phone.IsSpecified() {
		return nil, &badRequest{message: "Send at least one field to change."}
	}
	updated, err := s.account.Update(ctx, a, func(u *domain.User) {
		if body.FullName != nil {
			u.FullName = *body.FullName
		}
		patchString(body.Phone, &u.Phone)
	})
	if err != nil {
		return nil, err
	}
	return api.UpdateMe200JSONResponse(toMe(updated)), nil
}

func (s *Server) DeleteMe(ctx context.Context, _ api.DeleteMeRequestObject) (api.DeleteMeResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.account.Delete(ctx, a, requestMeta(ctx)); err != nil {
		return nil, err
	}
	return api.DeleteMe204Response{}, nil
}
