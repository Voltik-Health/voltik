package handler

import (
	"time"

	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/voltik/voltik/backend/internal/api"
	"github.com/voltik/voltik/backend/internal/domain"
	"github.com/voltik/voltik/backend/internal/service"
)

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func toGlucoseReading(r domain.GlucoseReading) api.GlucoseReading {
	return api.GlucoseReading{
		Id: r.ID, MeasuredAt: r.MeasuredAt.UTC(), ValueMgDl: r.ValueMgDl, Trend: api.GlucoseTrend(r.Trend),
		Source: api.GlucoseSource(r.Source), DeviceId: nullableOf(r.DeviceID), SignalQuality: nullableOf(r.SignalQuality),
		IdempotencyKey: nullableOf(r.IdempotencyKey), CreatedAt: r.CreatedAt.UTC(),
	}
}

func toHeartRate(h domain.HeartRateSample) api.HeartRateSample {
	return api.HeartRateSample{Id: h.ID, MeasuredAt: h.MeasuredAt.UTC(), Bpm: h.BPM, DeviceId: h.DeviceID, CreatedAt: h.CreatedAt.UTC()}
}

func toBatchResult(r domain.BatchResult) api.BatchResult {
	out := api.BatchResult{Inserted: r.Inserted, Duplicates: r.Duplicates, Rejected: make([]api.RejectedItem, 0, len(r.Rejected))}
	for _, it := range r.Rejected {
		out.Rejected = append(out.Rejected, api.RejectedItem{Index: it.Index, IdempotencyKey: nullableOf(it.IdempotencyKey), Code: it.Code, Message: it.Message})
	}
	return out
}

func toDevice(d domain.Device) api.Device {
	return api.Device{
		Id: d.ID, DeviceType: api.DeviceType(d.DeviceType), BrandModel: d.BrandModel, SerialOrMac: nullableOf(d.SerialOrMAC),
		ConnectionStatus: api.DeviceConnectionStatus(d.ConnectionStatus), BatteryPercent: nullableOf(d.BatteryPercent),
		FirstReadingAt: nullableOf(utc(d.FirstReadingAt)), SensorExpiresAt: nullableOf(utc(d.SensorExpiresAt)),
		LastSyncedAt: nullableOf(utc(d.LastSyncedAt)), CreatedAt: d.CreatedAt.UTC(), UpdatedAt: d.UpdatedAt.UTC(),
	}
}

func toAlert(a domain.Alert) api.Alert {
	out := api.Alert{
		Id: a.ID, AlertType: api.AlertType(a.Type), Severity: api.AlertSeverity(a.Severity), Title: a.Title, Message: a.Message,
		IrisSuggestedAction: nullableOf(a.IrisSuggestedAction), TriggeredAt: a.TriggeredAt.UTC(), AckStatus: api.AlertAckStatus(a.AckStatus),
		AcknowledgedAt: nullableOf(utc(a.AcknowledgedAt)), ResolutionNotes: nullableOf(a.ResolutionNotes),
		SourceReadingId: nullableOf(a.SourceReadingID), SourcePredictionId: nullableOf(a.SourcePredictionID),
		DeviceId: nullableOf(a.DeviceID), SourceValueMgDl: nullableOf(a.SourceValueMgDl), CreatedAt: a.CreatedAt.UTC(),
		AcknowledgedBy: nullable.NewNullNullable[api.AlertActor](), Recipients: make([]api.AlertRecipient, 0, len(a.Recipients)),
	}
	if a.AcknowledgedBy != nil {
		out.AcknowledgedBy = nullable.NewNullableWithValue(api.AlertActor{UserId: a.AcknowledgedBy.UserID, FullName: a.AcknowledgedBy.FullName})
	}
	for _, r := range a.Recipients {
		out.Recipients = append(out.Recipients, api.AlertRecipient{
			Id: r.ID, UserId: r.UserID, FullName: r.FullName, Channel: api.AlertChannel(r.Channel),
			DeliveryStatus: api.AlertDeliveryStatus(r.DeliveryStatus), SentAt: nullableOf(utc(r.SentAt)),
			DeliveredAt: nullableOf(utc(r.DeliveredAt)), ReadAt: nullableOf(utc(r.ReadAt)), AcknowledgedAt: nullableOf(utc(r.AcknowledgedAt)),
		})
	}
	return out
}

func toServing(s domain.FoodServing) api.FoodServing {
	return api.FoodServing{
		Id: s.ID, ServingDescription: s.Description, MetricAmount: nullableOf(s.MetricAmount),
		MetricUnit: nullableAs[api.MetricUnit](s.MetricUnit), CarbsG: s.CarbsG, FiberG: nullableOf(s.FiberG),
		SugarsG: nullableOf(s.SugarsG), CaloriesKcal: s.CaloriesKcal, ProteinG: s.ProteinG, FatG: s.FatG,
		SaturatedFatG: nullableOf(s.SaturatedFatG), SodiumMg: nullableOf(s.SodiumMg),
	}
}

func toFood(f domain.Food, caller domain.Account) api.Food {
	out := api.Food{
		Id: f.ID, FoodName: f.Name, Brand: nullableOf(f.Brand), FoodType: api.FoodType(f.Type), DataSource: api.FoodDataSource(f.DataSource),
		BarcodeEan: nullableOf(f.BarcodeEAN), IsVerified: f.IsVerified, IsOwn: f.CreatedBy != nil && *f.CreatedBy == caller.ID,
		SourceAttribution: nullableOf(domain.Attribution(f.DataSource)), Category: nullable.NewNullNullable[api.FoodCategoryRef](),
		Servings: make([]api.FoodServing, 0, len(f.Servings)), Components: make([]api.FoodComponent, 0, len(f.Components)),
		CreatedAt: f.CreatedAt.UTC(), UpdatedAt: f.UpdatedAt.UTC(),
	}
	if f.Category != nil {
		out.Category = nullable.NewNullableWithValue(api.FoodCategoryRef{Id: f.Category.ID, Name: f.Category.Name})
	}
	for _, s := range f.Servings {
		out.Servings = append(out.Servings, toServing(s))
	}
	for _, c := range f.Components {
		out.Components = append(out.Components, api.FoodComponent{Id: c.ID, ComponentFoodId: c.ComponentFoodID, FoodName: c.FoodName, DefaultQuantityG: c.DefaultGrams})
	}
	return out
}

func toCategory(c domain.FoodCategory) api.FoodCategory {
	return api.FoodCategory{Id: c.ID, Name: c.Name, FoodCount: c.FoodCount, CreatedAt: c.CreatedAt.UTC(), UpdatedAt: c.UpdatedAt.UTC()}
}

func toInsulin(l domain.InsulinLog) api.InsulinLog {
	return api.InsulinLog{
		Id: l.ID, InsulinType: api.InsulinType(l.InsulinType), InsulinBrand: nullableOf(l.Brand), DoseUnits: l.DoseUnits,
		AdministeredAt: l.AdministeredAt.UTC(), Reason: api.InsulinReason(l.Reason), MealId: nullableOf(l.MealID),
		Notes: nullableOf(l.Notes), IdempotencyKey: nullableOf(l.IdempotencyKey), EditedAfter24h: l.EditedAfter24h(),
		CreatedAt: l.CreatedAt.UTC(), UpdatedAt: l.UpdatedAt.UTC(),
	}
}

func toActivity(a domain.Activity) api.Activity {
	return api.Activity{
		Id: a.ID, ActivityName: a.Name, StartedAt: a.StartedAt.UTC(), DurationMinutes: a.DurationMinutes,
		Intensity: api.ActivityIntensity(a.Intensity), TotalSteps: nullableOf(a.TotalSteps), AvgHeartRateBpm: nullableOf(a.AvgHeartRateBPM),
		CaloriesBurnedKcal: nullableOf(a.CaloriesKcal), DeviceId: nullableOf(a.DeviceID), IdempotencyKey: nullableOf(a.IdempotencyKey),
		CreatedAt: a.CreatedAt.UTC(), UpdatedAt: a.UpdatedAt.UTC(),
	}
}

func toPhoto(p domain.MealPhoto, files *service.Files) api.MealPhoto {
	out := api.MealPhoto{
		Id: p.ID, MealId: p.MealID, AnalysisStatus: api.MealPhotoStatus(p.AnalysisStatus),
		EstimatedWeightG: nullableOf(p.EstimatedWeightG), EstimatedCarbsG: nullableOf(p.EstimatedCarbsG),
		EstimatedCaloriesKcal: nullableOf(p.EstimatedCaloriesKcal), ConfidencePercent: nullableOf(p.ConfidencePercent),
		LowConfidence: p.ConfidencePercent != nil && *p.ConfidencePercent < domain.LowConfidencePercent,
		AnalyzedAt:    nullableOf(utc(p.AnalyzedAt)), ConfirmedAt: nullableOf(utc(p.ConfirmedAt)), CreatedAt: p.CreatedAt.UTC(),
		ProposedItems: make([]api.MealPhotoProposedItem, 0, len(p.ProposedItems)),
	}
	if p.FileID != nil {
		out.ImageUrl, out.ImageUrlExpiresAt = files.Link(*p.FileID)
	}
	for _, it := range p.ProposedItems {
		out.ProposedItems = append(out.ProposedItems, api.MealPhotoProposedItem{
			Name: it.Name, EstimatedWeightG: nullableOf(it.EstimatedWeightG), EstimatedCarbsG: nullableOf(it.EstimatedCarbsG),
			EstimatedCaloriesKcal: nullableOf(it.EstimatedCaloriesKcal), FoodId: nullableOf(it.FoodID),
			ServingId: nullableOf(it.ServingID), ServingQuantity: nullableOf(it.ServingQuantity),
		})
	}
	return out
}

func toMeal(m domain.Meal, files *service.Files) api.Meal {
	out := api.Meal{
		Id: m.ID, MealType: api.MealType(m.MealType), MealDate: openapi_types.Date{Time: m.MealDate}, PlannedTime: nullableOf(m.PlannedTime),
		Status: api.MealStatus(m.Status), EatenAt: nullableOf(utc(m.EatenAt)), TotalCarbsG: m.TotalCarbsG, TotalCaloriesKcal: m.TotalCaloriesKcal,
		PostprandialDeltaMgDl: nullableOf(m.PostprandialDeltaMgDl), Notes: nullableOf(m.Notes), IdempotencyKey: nullableOf(m.IdempotencyKey),
		Items: make([]api.MealItem, 0, len(m.Items)), Photos: make([]api.MealPhoto, 0, len(m.Photos)),
		InsulinLogs: make([]api.InsulinLog, 0, len(m.InsulinLogs)), CreatedAt: m.CreatedAt.UTC(), UpdatedAt: m.UpdatedAt.UTC(),
	}
	for _, it := range m.Items {
		out.Items = append(out.Items, api.MealItem{
			Id: it.ID, FoodId: it.FoodID, FoodName: it.FoodName, ServingId: it.ServingID, ServingDescription: it.ServingDescription,
			ServingQuantity: it.Quantity, CarbsG: it.CarbsG, CaloriesKcal: it.CaloriesKcal, IsEaten: it.IsEaten, CreatedAt: it.CreatedAt.UTC(),
		})
	}
	for _, p := range m.Photos {
		out.Photos = append(out.Photos, toPhoto(p, files))
	}
	for _, l := range m.InsulinLogs {
		out.InsulinLogs = append(out.InsulinLogs, toInsulin(l))
	}
	return out
}

func toStats(s domain.Stats) api.GlucoseStats {
	return api.GlucoseStats{
		From: s.From.UTC(), To: s.To.UTC(), TargetMinMgDl: s.TargetMin, TargetMaxMgDl: s.TargetMax, ReadingsCount: s.ReadingsCount,
		CoveragePercent: s.CoveragePercent, TimeInRangePercent: nullableOf(s.TimeInRange), TimeBelowRangePercent: nullableOf(s.TimeBelowRange),
		TimeAboveRangePercent: nullableOf(s.TimeAboveRange), MeanGlucoseMgDl: nullableOf(s.MeanMgDl), StdDevMgDl: nullableOf(s.StdDevMgDl),
		CoefficientOfVariationPercent: nullableOf(s.CoefficientOfVar), GmiPercent: nullableOf(s.GMIPercent),
		HypoEpisodes: s.HypoEpisodes, HyperEpisodes: s.HyperEpisodes, ActiveMinutes: s.ActiveMinutes,
	}
}

func toReport(r domain.Report) api.ClinicalReport {
	out := api.ClinicalReport{
		Id: r.ID, PeriodType: api.ReportPeriodType(r.PeriodType), StartDate: openapi_types.Date{Time: r.StartDate},
		EndDate: openapi_types.Date{Time: r.EndDate}, GeneratedByRole: api.ReportGeneratedBy(r.GeneratedByRole),
		TargetMinMgDl: 70, TargetMaxMgDl: 180, LowCoverage: r.LowCoverage(),
		TimeInRangePercent: r.TimeInRange, TimeBelowRangePercent: r.TimeBelowRange, TimeAboveRangePercent: r.TimeAboveRange,
		MeanGlucoseMgDl: r.MeanMgDl, StdDevMgDl: nullableOf(r.StdDevMgDl), CoefficientOfVariationPercent: nullableOf(r.CoefficientOfVar),
		GmiPercent: nullableOf(r.GMIPercent), HypoEpisodes: r.HypoEpisodes, HyperEpisodes: r.HyperEpisodes,
		IrisSummary: nullableOf(r.IrisSummary), WorkImpactAnalysis: nullableOf(r.WorkImpactAnalysis), CreatedAt: r.CreatedAt.UTC(),
	}
	if r.TargetMin != nil && r.TargetMax != nil { // reports from before 000003 used the defaults
		out.TargetMinMgDl, out.TargetMaxMgDl = *r.TargetMin, *r.TargetMax
	}
	if r.CoveragePercent != nil {
		out.CoveragePercent = *r.CoveragePercent
	}
	return out
}

// nextCursor turns an empty cursor into null.
func nextCursor(c string) nullable.Nullable[string] {
	if c == "" {
		return nullable.NewNullNullable[string]()
	}
	return nullable.NewNullableWithValue(c)
}

func dateOf(t time.Time) openapi_types.Date { return openapi_types.Date{Time: t} }
