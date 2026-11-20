package handler

import (
	"context"

	"github.com/voltik/voltik/backend/internal/api"
	"github.com/voltik/voltik/backend/internal/domain"
	"github.com/voltik/voltik/backend/internal/service"
)

// --- Alerts and notifications -----------------------------------------------------------

func (s *Server) ListAlerts(ctx context.Context, req api.ListAlertsRequestObject) (api.ListAlertsResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	p := req.Params
	limit, err := limitOr(p.Limit, defaultListLimit, maxListLimit)
	if err != nil {
		return nil, err
	}
	f := domain.AlertFilter{From: utc(p.From), To: utc(p.To), Limit: limit, Cursor: cursorOf(p.Cursor)}
	var v domain.ValidationError
	if p.AckStatus != nil {
		checkEnum(&v, "ack_status", p.AckStatus)
		f.AckStatus = stringPtr(p.AckStatus)
	}
	if p.AlertType != nil {
		checkEnum(&v, "alert_type", p.AlertType)
		f.Type = stringPtr(p.AlertType)
	}
	if p.Severity != nil {
		checkEnum(&v, "severity", p.Severity)
		f.Severity = stringPtr(p.Severity)
	}
	if err := v.OrNil(); err != nil {
		return nil, err
	}
	items, next, err := s.alerts.List(ctx, a, f)
	if err != nil {
		return nil, err
	}
	out := api.AlertPage{Items: make([]api.Alert, 0, len(items)), NextCursor: nextCursor(next)}
	for _, al := range items {
		out.Items = append(out.Items, toAlert(al))
	}
	return api.ListAlerts200JSONResponse(out), nil
}

func (s *Server) AcknowledgeAlert(ctx context.Context, req api.AcknowledgeAlertRequestObject) (api.AcknowledgeAlertResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	al, err := s.alerts.Acknowledge(ctx, a, req.AlertId)
	if err != nil {
		return nil, err
	}
	return api.AcknowledgeAlert200JSONResponse(toAlert(al)), nil
}

func (s *Server) ResolveAlert(ctx context.Context, req api.ResolveAlertRequestObject) (api.ResolveAlertResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, noBody()
	}
	var notes *string
	patchValue(req.Body.ResolutionNotes, &notes)
	al, err := s.alerts.Resolve(ctx, a, req.AlertId, string(req.Body.Status), notes)
	if err != nil {
		return nil, err
	}
	return api.ResolveAlert200JSONResponse(toAlert(al)), nil
}

func (s *Server) MarkNotificationDelivered(ctx context.Context, req api.MarkNotificationDeliveredRequestObject) (api.MarkNotificationDeliveredResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.alerts.MarkNotification(ctx, a, req.RecipientId, domain.DeliveryDelivered); err != nil {
		return nil, err
	}
	return api.MarkNotificationDelivered204Response{}, nil
}

func (s *Server) MarkNotificationRead(ctx context.Context, req api.MarkNotificationReadRequestObject) (api.MarkNotificationReadResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.alerts.MarkNotification(ctx, a, req.RecipientId, domain.DeliveryRead); err != nil {
		return nil, err
	}
	return api.MarkNotificationRead204Response{}, nil
}

// --- Foods ---------------------------------------------------------------------------------

func (s *Server) SearchFoods(ctx context.Context, req api.SearchFoodsRequestObject) (api.SearchFoodsResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	limit, err := limitOr(req.Params.Limit, 20, 50)
	if err != nil {
		return nil, err
	}
	foods, err := s.foods.Search(ctx, a, req.Params.Q, limit)
	if err != nil {
		return nil, err
	}
	out := api.FoodList{Items: make([]api.Food, 0, len(foods))}
	for _, f := range foods {
		out.Items = append(out.Items, toFood(f, a))
	}
	return api.SearchFoods200JSONResponse(out), nil
}

func (s *Server) GetFood(ctx context.Context, req api.GetFoodRequestObject) (api.GetFoodResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	f, err := s.foods.Get(ctx, a, req.FoodId)
	if err != nil {
		return nil, err
	}
	return api.GetFood200JSONResponse(toFood(f, a)), nil
}

func (s *Server) GetFoodByBarcode(ctx context.Context, req api.GetFoodByBarcodeRequestObject) (api.GetFoodByBarcodeResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	f, err := s.foods.ByBarcode(ctx, a, req.Ean)
	if err != nil {
		return nil, err
	}
	return api.GetFoodByBarcode200JSONResponse(toFood(f, a)), nil
}

func servingFrom(in api.ServingInput) domain.FoodServing {
	return domain.FoodServing{
		Description: in.ServingDescription, MetricAmount: in.MetricAmount, MetricUnit: stringPtr(in.MetricUnit),
		Nutrition: domain.Nutrition{
			CarbsG: in.CarbsG, FiberG: in.FiberG, SugarsG: in.SugarsG, CaloriesKcal: in.CaloriesKcal,
			ProteinG: valueOr(in.ProteinG), FatG: valueOr(in.FatG), SaturatedFatG: in.SaturatedFatG, SodiumMg: in.SodiumMg,
		},
	}
}

func valueOr(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func categoryRef(id *api.CategoryId) *domain.FoodCategoryRef {
	if id == nil {
		return nil
	}
	return &domain.FoodCategoryRef{ID: *id}
}

func (s *Server) CreateFood(ctx context.Context, req api.CreateFoodRequestObject) (api.CreateFoodResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if b == nil {
		return nil, noBody()
	}
	f := domain.Food{Name: b.FoodName, Brand: b.Brand, BarcodeEAN: b.BarcodeEan, Category: categoryRef(b.CategoryId)}
	if b.FoodType != nil {
		f.Type = string(*b.FoodType)
	}
	for _, sv := range b.Servings {
		f.Servings = append(f.Servings, servingFrom(sv))
	}
	created, err := s.foods.CreateCustom(ctx, a, f)
	if err != nil {
		return nil, err
	}
	return api.CreateFood201JSONResponse(toFood(created, a)), nil
}

func (s *Server) ListFoodCategories(ctx context.Context, _ api.ListFoodCategoriesRequestObject) (api.ListFoodCategoriesResponseObject, error) {
	if _, err := account(ctx); err != nil {
		return nil, err
	}
	cats, err := s.foods.Categories(ctx)
	if err != nil {
		return nil, err
	}
	out := api.FoodCategoryList{Items: make([]api.FoodCategory, 0, len(cats))}
	for _, c := range cats {
		out.Items = append(out.Items, toCategory(c))
	}
	return api.ListFoodCategories200JSONResponse(out), nil
}

// --- Food catalogue (admin) ---------------------------------------------------------------

func (s *Server) CreateFoodCategory(ctx context.Context, req api.CreateFoodCategoryRequestObject) (api.CreateFoodCategoryResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, noBody()
	}
	c, err := s.foods.CreateCategory(ctx, a, req.Body.Name)
	if err != nil {
		return nil, err
	}
	return api.CreateFoodCategory201JSONResponse(toCategory(c)), nil
}

func (s *Server) RenameFoodCategory(ctx context.Context, req api.RenameFoodCategoryRequestObject) (api.RenameFoodCategoryResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, noBody()
	}
	c, err := s.foods.RenameCategory(ctx, a, req.CategoryId, req.Body.Name)
	if err != nil {
		return nil, err
	}
	return api.RenameFoodCategory200JSONResponse(toCategory(c)), nil
}

func (s *Server) DeleteFoodCategory(ctx context.Context, req api.DeleteFoodCategoryRequestObject) (api.DeleteFoodCategoryResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.foods.DeleteCategory(ctx, a, req.CategoryId); err != nil {
		return nil, err
	}
	return api.DeleteFoodCategory204Response{}, nil
}

func (s *Server) ListFoodsForReview(ctx context.Context, req api.ListFoodsForReviewRequestObject) (api.ListFoodsForReviewResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	limit, err := limitOr(req.Params.Limit, defaultListLimit, maxListLimit)
	if err != nil {
		return nil, err
	}
	foods, next, err := s.foods.ForReview(ctx, a, req.Params.Verified, req.Params.Q, limit, cursorOf(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out := api.AdminFoodPage{Items: make([]api.AdminFoodItem, 0, len(foods)), NextCursor: nextCursor(next)}
	for _, f := range foods {
		out.Items = append(out.Items, api.AdminFoodItem{Food: toFood(f, a), UsageCount: f.UsageCount})
	}
	return api.ListFoodsForReview200JSONResponse(out), nil
}

func (s *Server) AdminCreateFood(ctx context.Context, req api.AdminCreateFoodRequestObject) (api.AdminCreateFoodResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if b == nil {
		return nil, noBody()
	}
	f := domain.Food{Name: b.FoodName, Brand: b.Brand, Type: string(b.FoodType), BarcodeEAN: b.BarcodeEan, Category: categoryRef(b.CategoryId)}
	if b.Servings != nil {
		for _, sv := range *b.Servings {
			f.Servings = append(f.Servings, servingFrom(sv))
		}
	}
	created, err := s.foods.AdminCreate(ctx, a, f)
	if err != nil {
		return nil, err
	}
	return api.AdminCreateFood201JSONResponse(toFood(created, a)), nil
}

func (s *Server) AdminUpdateFood(ctx context.Context, req api.AdminUpdateFoodRequestObject) (api.AdminUpdateFoodResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if b == nil || (b.FoodName == nil && !b.Brand.IsSpecified() && !b.BarcodeEan.IsSpecified() && !b.CategoryId.IsSpecified()) {
		return nil, &badRequest{message: "Send at least one field to change."}
	}
	c := service.FoodChange{Name: b.FoodName, SetBrand: b.Brand.IsSpecified(), SetBarcode: b.BarcodeEan.IsSpecified(), SetCategory: b.CategoryId.IsSpecified()}
	patchValue(b.Brand, &c.Brand)
	patchValue(b.BarcodeEan, &c.Barcode)
	patchValue(b.CategoryId, &c.CategoryID)
	f, err := s.foods.AdminUpdate(ctx, a, req.FoodId, c)
	if err != nil {
		return nil, err
	}
	return api.AdminUpdateFood200JSONResponse(toFood(f, a)), nil
}

func (s *Server) AdminDeleteFood(ctx context.Context, req api.AdminDeleteFoodRequestObject) (api.AdminDeleteFoodResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.foods.AdminDelete(ctx, a, req.FoodId); err != nil {
		return nil, err
	}
	return api.AdminDeleteFood204Response{}, nil
}

func (s *Server) AddFoodComponent(ctx context.Context, req api.AddFoodComponentRequestObject) (api.AddFoodComponentResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, noBody()
	}
	f, err := s.foods.AddComponent(ctx, a, req.FoodId, req.Body.ComponentFoodId, req.Body.DefaultQuantityG)
	if err != nil {
		return nil, err
	}
	return api.AddFoodComponent201JSONResponse(toFood(f, a)), nil
}

func (s *Server) RemoveFoodComponent(ctx context.Context, req api.RemoveFoodComponentRequestObject) (api.RemoveFoodComponentResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	f, err := s.foods.RemoveComponent(ctx, a, req.FoodId, req.ComponentId)
	if err != nil {
		return nil, err
	}
	return api.RemoveFoodComponent200JSONResponse(toFood(f, a)), nil
}

func (s *Server) AddFoodServing(ctx context.Context, req api.AddFoodServingRequestObject) (api.AddFoodServingResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, noBody()
	}
	sv, err := s.foods.AddServing(ctx, a, req.FoodId, servingFrom(*req.Body))
	if err != nil {
		return nil, err
	}
	return api.AddFoodServing201JSONResponse(toServing(sv)), nil
}

func (s *Server) UpdateFoodServing(ctx context.Context, req api.UpdateFoodServingRequestObject) (api.UpdateFoodServingResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if b == nil {
		return nil, noBody()
	}
	sv, err := s.foods.UpdateServing(ctx, a, req.FoodId, req.ServingId, func(sv *domain.FoodServing) {
		if b.ServingDescription != nil {
			sv.Description = *b.ServingDescription
		}
		patchValue(b.MetricAmount, &sv.MetricAmount)
		patchString(b.MetricUnit, &sv.MetricUnit)
		if b.CarbsG != nil {
			sv.CarbsG = *b.CarbsG
		}
		patchValue(b.FiberG, &sv.FiberG)
		patchValue(b.SugarsG, &sv.SugarsG)
		if b.CaloriesKcal != nil {
			sv.CaloriesKcal = *b.CaloriesKcal
		}
		if b.ProteinG != nil {
			sv.ProteinG = *b.ProteinG
		}
		if b.FatG != nil {
			sv.FatG = *b.FatG
		}
		patchValue(b.SaturatedFatG, &sv.SaturatedFatG)
		patchValue(b.SodiumMg, &sv.SodiumMg)
	})
	if err != nil {
		return nil, err
	}
	return api.UpdateFoodServing200JSONResponse(toServing(sv)), nil
}

func (s *Server) DeleteFoodServing(ctx context.Context, req api.DeleteFoodServingRequestObject) (api.DeleteFoodServingResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.foods.DeleteServing(ctx, a, req.FoodId, req.ServingId); err != nil {
		return nil, err
	}
	return api.DeleteFoodServing204Response{}, nil
}

func (s *Server) VerifyFood(ctx context.Context, req api.VerifyFoodRequestObject) (api.VerifyFoodResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	f, err := s.foods.Verify(ctx, a, req.FoodId)
	if err != nil {
		return nil, err
	}
	return api.VerifyFood200JSONResponse(toFood(f, a)), nil
}
