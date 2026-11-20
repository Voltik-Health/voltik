package handler

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/voltik/voltik/backend/internal/api"
	"github.com/voltik/voltik/backend/internal/domain"
	"github.com/voltik/voltik/backend/internal/service"
)

// --- Meals ---------------------------------------------------------------------------------

func itemsFrom(in []api.MealItemInput) []domain.MealItemInput {
	out := make([]domain.MealItemInput, len(in))
	for i, it := range in {
		out[i] = domain.MealItemInput{ID: it.Id, ServingID: it.ServingId, Quantity: 1, IsEaten: true}
		if it.ServingQuantity != nil {
			out[i].Quantity = *it.ServingQuantity
		}
		if it.IsEaten != nil {
			out[i].IsEaten = *it.IsEaten
		}
	}
	return out
}

func (s *Server) ListMeals(ctx context.Context, req api.ListMealsRequestObject) (api.ListMealsResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	p := req.Params
	var from, to time.Time
	switch {
	case p.Date != nil && p.FromDate == nil && p.ToDate == nil:
		from, to = p.Date.Time, p.Date.Time
	case p.Date == nil && p.FromDate != nil && p.ToDate != nil:
		from, to = p.FromDate.Time, p.ToDate.Time
	default:
		return nil, &badRequest{message: "Send date, or from_date and to_date."}
	}
	meals, err := s.diary.ListMeals(ctx, a, from, to)
	if err != nil {
		return nil, err
	}
	out := api.MealList{Items: make([]api.Meal, 0, len(meals))}
	for _, m := range meals {
		out.Items = append(out.Items, toMeal(m, s.files))
	}
	return api.ListMeals200JSONResponse(out), nil
}

func (s *Server) CreateMeal(ctx context.Context, req api.CreateMealRequestObject) (api.CreateMealResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if b == nil {
		return nil, noBody()
	}
	n := service.NewMeal{
		IdempotencyKey: b.IdempotencyKey, MealType: string(b.MealType), Status: string(b.Status), PlannedTime: b.PlannedTime,
		EatenAt: utc(b.EatenAt), Notes: b.Notes, TotalCarbsG: b.TotalCarbsG, TotalKcal: b.TotalCaloriesKcal,
	}
	if b.MealDate != nil {
		d := b.MealDate.Time
		n.MealDate = &d
	}
	if b.Items != nil {
		n.Items = itemsFrom(*b.Items)
	}
	m, created, err := s.diary.CreateMeal(ctx, a, n)
	if err != nil {
		return nil, err
	}
	if !created {
		return api.CreateMeal200JSONResponse(toMeal(m, s.files)), nil
	}
	return api.CreateMeal201JSONResponse(toMeal(m, s.files)), nil
}

func (s *Server) GetMeal(ctx context.Context, req api.GetMealRequestObject) (api.GetMealResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	m, err := s.diary.GetMeal(ctx, a, req.MealId)
	if err != nil {
		return nil, err
	}
	return api.GetMeal200JSONResponse(toMeal(m, s.files)), nil
}

func (s *Server) UpdateMeal(ctx context.Context, req api.UpdateMealRequestObject) (api.UpdateMealResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if b == nil || (b.MealType == nil && b.MealDate == nil && !b.PlannedTime.IsSpecified() && b.EatenAt == nil &&
		!b.Notes.IsSpecified() && b.TotalCarbsG == nil && b.TotalCaloriesKcal == nil) {
		return nil, &badRequest{message: "Send at least one field to change."}
	}
	c := service.MealChange{
		EatenAt: utc(b.EatenAt), SetPlannedTime: b.PlannedTime.IsSpecified(), SetNotes: b.Notes.IsSpecified(),
		TotalCarbsG: b.TotalCarbsG, TotalKcal: b.TotalCaloriesKcal,
	}
	if b.MealType != nil {
		t := string(*b.MealType)
		c.MealType = &t
	}
	if b.MealDate != nil {
		d := b.MealDate.Time
		c.MealDate = &d
	}
	patchValue(b.PlannedTime, &c.PlannedTime)
	patchValue(b.Notes, &c.Notes)
	m, err := s.diary.UpdateMeal(ctx, a, req.MealId, c)
	if err != nil {
		return nil, err
	}
	return api.UpdateMeal200JSONResponse(toMeal(m, s.files)), nil
}

func (s *Server) DeleteMeal(ctx context.Context, req api.DeleteMealRequestObject) (api.DeleteMealResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.diary.DeleteMeal(ctx, a, req.MealId); err != nil {
		return nil, err
	}
	return api.DeleteMeal204Response{}, nil
}

func (s *Server) ReplaceMealItems(ctx context.Context, req api.ReplaceMealItemsRequestObject) (api.ReplaceMealItemsResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, noBody()
	}
	m, err := s.diary.ReplaceItems(ctx, a, req.MealId, itemsFrom(req.Body.Items))
	if err != nil {
		return nil, err
	}
	return api.ReplaceMealItems200JSONResponse(toMeal(m, s.files)), nil
}

func (s *Server) MarkMealEaten(ctx context.Context, req api.MarkMealEatenRequestObject) (api.MarkMealEatenResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	var at *time.Time
	if req.Body != nil {
		at = utc(req.Body.EatenAt)
	}
	m, err := s.diary.MarkEaten(ctx, a, req.MealId, at)
	if err != nil {
		return nil, err
	}
	return api.MarkMealEaten200JSONResponse(toMeal(m, s.files)), nil
}

func (s *Server) CancelMeal(ctx context.Context, req api.CancelMealRequestObject) (api.CancelMealResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	m, err := s.diary.CancelMeal(ctx, a, req.MealId)
	if err != nil {
		return nil, err
	}
	return api.CancelMeal200JSONResponse(toMeal(m, s.files)), nil
}

// --- Meal photos ----------------------------------------------------------------------------

func (s *Server) UploadMealPhoto(ctx context.Context, req api.UploadMealPhotoRequestObject) (api.UploadMealPhotoResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, noBody()
	}
	var data []byte
	for {
		part, err := req.Body.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, &badRequest{message: "The multipart body is not valid."}
		}
		if part.FormName() != "photo" {
			continue
		}
		data, err = io.ReadAll(io.LimitReader(part, service.MaxPhotoBytes+1))
		if err != nil {
			return nil, err
		}
		break
	}
	if len(data) == 0 {
		return nil, domain.Invalid("photo", "is required")
	}
	if len(data) > service.MaxPhotoBytes {
		return nil, domain.ErrPayloadTooLarge
	}
	p, err := s.diary.UploadPhoto(ctx, a, req.MealId, data)
	if err != nil {
		return nil, err
	}
	return api.UploadMealPhoto202JSONResponse(toPhoto(p, s.files)), nil
}

func (s *Server) GetMealPhoto(ctx context.Context, req api.GetMealPhotoRequestObject) (api.GetMealPhotoResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.diary.GetPhoto(ctx, a, req.MealId, req.PhotoId)
	if err != nil {
		return nil, err
	}
	return api.GetMealPhoto200JSONResponse(toPhoto(p, s.files)), nil
}

func (s *Server) ConfirmMealPhoto(ctx context.Context, req api.ConfirmMealPhotoRequestObject) (api.ConfirmMealPhotoResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, noBody()
	}
	m, err := s.diary.ConfirmPhoto(ctx, a, req.MealId, req.PhotoId, itemsFrom(req.Body.Items))
	if err != nil {
		return nil, err
	}
	return api.ConfirmMealPhoto200JSONResponse(toMeal(m, s.files)), nil
}

// --- Insulin ---------------------------------------------------------------------------------

func (s *Server) ListInsulinLogs(ctx context.Context, req api.ListInsulinLogsRequestObject) (api.ListInsulinLogsResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	logs, totals, err := s.diary.ListInsulin(ctx, a, req.Params.From, req.Params.To)
	if err != nil {
		return nil, err
	}
	out := api.InsulinLogList{Items: make([]api.InsulinLog, 0, len(logs)), DailyTotals: make([]api.InsulinDailyTotal, 0, len(totals))}
	for _, l := range logs {
		out.Items = append(out.Items, toInsulin(l))
	}
	for _, t := range totals {
		out.DailyTotals = append(out.DailyTotals, api.InsulinDailyTotal{
			Date: dateOf(t.Date), InsulinType: api.InsulinType(t.InsulinType), TotalUnits: t.TotalUnits, Doses: t.Doses,
		})
	}
	return api.ListInsulinLogs200JSONResponse(out), nil
}

func (s *Server) CreateInsulinLog(ctx context.Context, req api.CreateInsulinLogRequestObject) (api.CreateInsulinLogResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if b == nil {
		return nil, noBody()
	}
	key := b.IdempotencyKey
	l, created, err := s.diary.CreateInsulin(ctx, a, domain.InsulinLog{
		InsulinType: string(b.InsulinType), Brand: b.InsulinBrand, DoseUnits: b.DoseUnits, AdministeredAt: b.AdministeredAt.UTC(),
		Reason: string(b.Reason), MealID: b.MealId, Notes: b.Notes, IdempotencyKey: &key,
	})
	if err != nil {
		return nil, err
	}
	if !created {
		return api.CreateInsulinLog200JSONResponse(toInsulin(l)), nil
	}
	return api.CreateInsulinLog201JSONResponse(toInsulin(l)), nil
}

func (s *Server) UpdateInsulinLog(ctx context.Context, req api.UpdateInsulinLogRequestObject) (api.UpdateInsulinLogResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if b == nil || (b.InsulinType == nil && !b.InsulinBrand.IsSpecified() && b.DoseUnits == nil && b.AdministeredAt == nil &&
		b.Reason == nil && !b.MealId.IsSpecified() && !b.Notes.IsSpecified()) {
		return nil, &badRequest{message: "Send at least one field to change."}
	}
	l, err := s.diary.UpdateInsulin(ctx, a, req.InsulinLogId, func(l *domain.InsulinLog) {
		if b.InsulinType != nil {
			l.InsulinType = string(*b.InsulinType)
		}
		patchValue(b.InsulinBrand, &l.Brand)
		if b.DoseUnits != nil {
			l.DoseUnits = *b.DoseUnits
		}
		if b.AdministeredAt != nil {
			l.AdministeredAt = b.AdministeredAt.UTC()
		}
		if b.Reason != nil {
			l.Reason = string(*b.Reason)
		}
		patchValue(b.MealId, &l.MealID)
		patchValue(b.Notes, &l.Notes)
	})
	if err != nil {
		return nil, err
	}
	return api.UpdateInsulinLog200JSONResponse(toInsulin(l)), nil
}

func (s *Server) DeleteInsulinLog(ctx context.Context, req api.DeleteInsulinLogRequestObject) (api.DeleteInsulinLogResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.diary.DeleteInsulin(ctx, a, req.InsulinLogId); err != nil {
		return nil, err
	}
	return api.DeleteInsulinLog204Response{}, nil
}

// --- Activities --------------------------------------------------------------------------------

func activityFrom(b api.ActivityCreate) domain.Activity {
	key := b.IdempotencyKey
	return domain.Activity{
		Name: b.ActivityName, StartedAt: b.StartedAt.UTC(), DurationMinutes: b.DurationMinutes, Intensity: string(b.Intensity),
		TotalSteps: b.TotalSteps, AvgHeartRateBPM: b.AvgHeartRateBpm, CaloriesKcal: b.CaloriesBurnedKcal,
		DeviceID: b.DeviceId, IdempotencyKey: &key,
	}
}

func (s *Server) ListActivities(ctx context.Context, req api.ListActivitiesRequestObject) (api.ListActivitiesResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	items, err := s.diary.ListActivities(ctx, a, req.Params.From, req.Params.To)
	if err != nil {
		return nil, err
	}
	out := api.ActivityList{Items: make([]api.Activity, 0, len(items))}
	for _, it := range items {
		out.Items = append(out.Items, toActivity(it))
	}
	return api.ListActivities200JSONResponse(out), nil
}

func (s *Server) CreateActivity(ctx context.Context, req api.CreateActivityRequestObject) (api.CreateActivityResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, noBody()
	}
	act, created, err := s.diary.CreateActivity(ctx, a, activityFrom(*req.Body))
	if err != nil {
		return nil, err
	}
	if !created {
		return api.CreateActivity200JSONResponse(toActivity(act)), nil
	}
	return api.CreateActivity201JSONResponse(toActivity(act)), nil
}

func (s *Server) IngestActivities(ctx context.Context, req api.IngestActivitiesRequestObject) (api.IngestActivitiesResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil || len(req.Body.Activities) == 0 || len(req.Body.Activities) > domain.MaxBatchItems {
		return nil, &badRequest{message: "Send between 1 and 500 activities."}
	}
	in := make([]domain.Activity, len(req.Body.Activities))
	for i, b := range req.Body.Activities {
		in[i] = activityFrom(b)
	}
	res, err := s.diary.IngestActivities(ctx, a, in)
	if err != nil {
		return nil, err
	}
	return api.IngestActivities200JSONResponse(toBatchResult(res)), nil
}

func (s *Server) UpdateActivity(ctx context.Context, req api.UpdateActivityRequestObject) (api.UpdateActivityResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if b == nil || (b.ActivityName == nil && b.StartedAt == nil && b.DurationMinutes == nil && b.Intensity == nil &&
		!b.TotalSteps.IsSpecified() && !b.AvgHeartRateBpm.IsSpecified() && !b.CaloriesBurnedKcal.IsSpecified()) {
		return nil, &badRequest{message: "Send at least one field to change."}
	}
	act, err := s.diary.UpdateActivity(ctx, a, req.ActivityId, func(act *domain.Activity) {
		if b.ActivityName != nil {
			act.Name = *b.ActivityName
		}
		if b.StartedAt != nil {
			act.StartedAt = b.StartedAt.UTC()
		}
		if b.DurationMinutes != nil {
			act.DurationMinutes = *b.DurationMinutes
		}
		if b.Intensity != nil {
			act.Intensity = string(*b.Intensity)
		}
		patchValue(b.TotalSteps, &act.TotalSteps)
		patchValue(b.AvgHeartRateBpm, &act.AvgHeartRateBPM)
		patchValue(b.CaloriesBurnedKcal, &act.CaloriesKcal)
	})
	if err != nil {
		return nil, err
	}
	return api.UpdateActivity200JSONResponse(toActivity(act)), nil
}

func (s *Server) DeleteActivity(ctx context.Context, req api.DeleteActivityRequestObject) (api.DeleteActivityResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.diary.DeleteActivity(ctx, a, req.ActivityId); err != nil {
		return nil, err
	}
	return api.DeleteActivity204Response{}, nil
}

// --- Statistics and reports -----------------------------------------------------------------

func (s *Server) GetGlucoseStats(ctx context.Context, req api.GetGlucoseStatsRequestObject) (api.GetGlucoseStatsResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	st, err := s.history.Stats(ctx, a, req.Params.From, req.Params.To)
	if err != nil {
		return nil, err
	}
	return api.GetGlucoseStats200JSONResponse(toStats(st)), nil
}

func (s *Server) ListReports(ctx context.Context, req api.ListReportsRequestObject) (api.ListReportsResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	limit, err := limitOr(req.Params.Limit, defaultListLimit, maxListLimit)
	if err != nil {
		return nil, err
	}
	reports, next, err := s.history.ListReports(ctx, a, limit, cursorOf(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out := api.ReportPage{Items: make([]api.ClinicalReport, 0, len(reports)), NextCursor: nextCursor(next)}
	for _, r := range reports {
		out.Items = append(out.Items, toReport(r))
	}
	return api.ListReports200JSONResponse(out), nil
}

func (s *Server) CreateReport(ctx context.Context, req api.CreateReportRequestObject) (api.CreateReportResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if b == nil {
		return nil, noBody()
	}
	r, err := s.history.CreateReport(ctx, a, string(b.PeriodType), b.StartDate.Time, b.EndDate.Time)
	if err != nil {
		return nil, err
	}
	return api.CreateReport201JSONResponse(toReport(r)), nil
}

func (s *Server) GetReport(ctx context.Context, req api.GetReportRequestObject) (api.GetReportResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.history.GetReport(ctx, a, req.ReportId)
	if err != nil {
		return nil, err
	}
	return api.GetReport200JSONResponse(toReport(r)), nil
}

func (s *Server) DeleteReport(ctx context.Context, req api.DeleteReportRequestObject) (api.DeleteReportResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.history.DeleteReport(ctx, a, req.ReportId); err != nil {
		return nil, err
	}
	return api.DeleteReport204Response{}, nil
}

func (s *Server) ExportReportPdf(ctx context.Context, req api.ExportReportPdfRequestObject) (api.ExportReportPdfResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	url, expires, err := s.history.ExportPDF(ctx, a, req.ReportId)
	if err != nil {
		return nil, err
	}
	return api.ExportReportPdf200JSONResponse(api.FileLink{Url: url, ExpiresAt: expires}), nil
}
