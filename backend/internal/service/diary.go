package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png" // decoders for the uploaded photos
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/voltik/voltik/backend/internal/domain"
	"github.com/voltik/voltik/backend/internal/gemini"
)

// DiaryStore is what the diary needs from the database.
type DiaryStore interface {
	PatientCard(ctx context.Context, patientID uuid.UUID) (domain.PatientCard, error)
	HasActiveConsent(ctx context.Context, userID uuid.UUID, docType string) (bool, error)
	ReadingPoints(ctx context.Context, patientID uuid.UUID, from, to time.Time) ([]domain.Point, error)
	SearchFoods(ctx context.Context, userID uuid.UUID, q string, limit int) ([]domain.Food, error)
	GetFile(ctx context.Context, id uuid.UUID) (domain.StoredFile, error)

	CreateMeal(ctx context.Context, userID uuid.UUID, m domain.Meal, items []domain.MealItemInput) (domain.Meal, bool, error)
	GetMeal(ctx context.Context, patientID, mealID uuid.UUID) (domain.Meal, error)
	ListMeals(ctx context.Context, patientID uuid.UUID, from, to time.Time) ([]domain.Meal, error)
	UpdateMeal(ctx context.Context, patientID, mealID uuid.UUID, apply func(*domain.Meal) error) (domain.Meal, error)
	ReplaceMealItems(ctx context.Context, userID, patientID, mealID uuid.UUID, items []domain.MealItemInput) (domain.Meal, error)
	MarkMealEaten(ctx context.Context, patientID, mealID uuid.UUID, eatenAt time.Time) (domain.Meal, bool, error)
	CancelMeal(ctx context.Context, patientID, mealID uuid.UUID) (domain.Meal, error)
	DeleteMeal(ctx context.Context, patientID, mealID uuid.UUID) error
	MealsDueForImpact(ctx context.Context, now time.Time, limit int) ([]domain.Meal, error)
	SetPostprandial(ctx context.Context, mealID uuid.UUID, delta *int) error

	CreateMealPhoto(ctx context.Context, patientID, mealID uuid.UUID, file domain.StoredFile) (domain.MealPhoto, error)
	GetMealPhoto(ctx context.Context, patientID, mealID, photoID uuid.UUID) (domain.MealPhoto, error)
	ClaimPhotoAnalyses(ctx context.Context, now time.Time, limit int) ([]domain.MealPhoto, error)
	CompletePhotoAnalysis(ctx context.Context, photoID uuid.UUID, a domain.PhotoAnalysis) error
	FailPhotoAnalysis(ctx context.Context, photoID uuid.UUID) error
	ConfirmMealPhoto(ctx context.Context, userID, patientID, mealID, photoID uuid.UUID, items []domain.MealItemInput) (domain.Meal, error)

	CreateInsulinLog(ctx context.Context, l domain.InsulinLog) (domain.InsulinLog, bool, error)
	UpdateInsulinLog(ctx context.Context, patientID, id uuid.UUID, apply func(*domain.InsulinLog) error) (domain.InsulinLog, error)
	DeleteInsulinLog(ctx context.Context, patientID, id uuid.UUID) error
	ListInsulinLogs(ctx context.Context, patientID uuid.UUID, from, to time.Time) ([]domain.InsulinLog, []domain.InsulinDailyTotal, error)

	CreateActivity(ctx context.Context, a domain.Activity) (domain.Activity, bool, error)
	IngestActivities(ctx context.Context, patientID uuid.UUID, in []domain.Activity) ([]int, error)
	UpdateActivity(ctx context.Context, patientID, id uuid.UUID, apply func(*domain.Activity) error) (domain.Activity, error)
	DeleteActivity(ctx context.Context, patientID, id uuid.UUID) error
	ListActivities(ctx context.Context, patientID uuid.UUID, from, to time.Time) ([]domain.Activity, error)
}

// AI generates structured answers (Gemini).
type AI interface {
	Configured() bool
	GenerateJSON(ctx context.Context, prompt string, image *gemini.Image, schema map[string]any, out any) (string, error)
}

// Diary runs meals, photos, insulin and activity (SRS UC089–UC111).
type Diary struct {
	store    DiaryStore
	ai       AI
	notifier Notifier
	analysis Waker
	log      *slog.Logger
	now      func() time.Time
}

func NewDiary(store DiaryStore, ai AI, notifier Notifier, log *slog.Logger) *Diary {
	return &Diary{store: store, ai: ai, notifier: notifier, log: log, now: time.Now}
}

// SetAnalysisWaker tells the diary which worker analyses the photos.
func (d *Diary) SetAnalysisWaker(w Waker) { d.analysis = w }

// --- Meals -------------------------------------------------------------------------

// NewMeal is a meal sent by the app.
type NewMeal struct {
	IdempotencyKey *string
	MealType       string
	Status         string
	MealDate       *time.Time
	PlannedTime    *string
	EatenAt        *time.Time
	Notes          *string
	Items          []domain.MealItemInput
	TotalCarbsG    *float64
	TotalKcal      *float64
}

// CreateMeal plans or logs a meal (UC100, UC101).
func (d *Diary) CreateMeal(ctx context.Context, a domain.Account, n NewMeal) (domain.Meal, bool, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.Meal{}, false, err
	}
	now := d.now()
	m := domain.Meal{PatientID: pid, MealType: n.MealType, Status: n.Status, PlannedTime: n.PlannedTime, Notes: orNil(n.Notes), IdempotencyKey: n.IdempotencyKey}
	var v domain.ValidationError
	switch n.Status {
	case domain.MealEaten:
		at := now
		if n.EatenAt != nil {
			at = *n.EatenAt
		}
		m.EatenAt = &at
		m.MealDate = domain.LisbonDate(at)
		if n.MealDate != nil {
			m.MealDate = *n.MealDate
		}
	case domain.MealPlanned:
		if n.MealDate == nil {
			v.Add("meal_date", "is required for a planned meal")
		} else {
			m.MealDate = *n.MealDate
		}
		if n.EatenAt != nil {
			v.Add("eaten_at", "only eaten meals have eaten_at")
		}
	default:
		v.Add("status", "must be planned or eaten")
	}
	if len(n.Items) > 0 && (n.TotalCarbsG != nil || n.TotalKcal != nil) {
		v.Add("total_carbs_g", "only for a meal without items: the totals come from the items")
	}
	if n.TotalCarbsG != nil {
		m.TotalCarbsG = *n.TotalCarbsG
	}
	if n.TotalKcal != nil {
		m.TotalCaloriesKcal = *n.TotalKcal
	}
	if n.IdempotencyKey != nil && (strings.TrimSpace(*n.IdempotencyKey) == "" || utf8.RuneCountInString(*n.IdempotencyKey) > 100) {
		v.Add("idempotency_key", "must have between 1 and 100 characters")
	}
	if err := v.OrNil(); err != nil {
		return m, false, err
	}
	if err := domain.ValidateMeal(m, now); err != nil {
		return m, false, err
	}
	if err := domain.ValidateItems("items", n.Items, false); err != nil {
		return m, false, err
	}
	return d.store.CreateMeal(ctx, a.ID, m, n.Items)
}

// orNil trims an optional text and turns a blank one into nil.
func orNil(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

func (d *Diary) GetMeal(ctx context.Context, a domain.Account, id uuid.UUID) (domain.Meal, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.Meal{}, err
	}
	return d.store.GetMeal(ctx, pid, id)
}

// ListMeals returns the meals of the days [from, to] (UC107).
func (d *Diary) ListMeals(ctx context.Context, a domain.Account, from, to time.Time) ([]domain.Meal, error) {
	pid, err := patientOf(a)
	if err != nil {
		return nil, err
	}
	if err := domain.CheckDateRange("from_date", "to_date", from, to); err != nil {
		return nil, err
	}
	return d.store.ListMeals(ctx, pid, from, to)
}

// MealChange is a PATCH of a meal: nil leaves a field as it is.
type MealChange struct {
	MealType       *string
	MealDate       *time.Time
	SetPlannedTime bool
	PlannedTime    *string
	EatenAt        *time.Time
	SetNotes       bool
	Notes          *string
	TotalCarbsG    *float64
	TotalKcal      *float64
}

func (d *Diary) UpdateMeal(ctx context.Context, a domain.Account, id uuid.UUID, c MealChange) (domain.Meal, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.Meal{}, err
	}
	now := d.now()
	return d.store.UpdateMeal(ctx, pid, id, func(m *domain.Meal) error {
		if c.MealType != nil {
			m.MealType = *c.MealType
		}
		if c.MealDate != nil {
			m.MealDate = *c.MealDate
		}
		if c.SetPlannedTime {
			m.PlannedTime = c.PlannedTime
		}
		if c.EatenAt != nil {
			if m.Status != domain.MealEaten {
				return domain.InvalidState("Only an eaten meal has eaten_at. Confirm it with POST /v1/me/meals/{meal_id}/eaten.")
			}
			m.EatenAt = c.EatenAt
		}
		if c.SetNotes {
			m.Notes = orNil(c.Notes)
		}
		if c.TotalCarbsG != nil || c.TotalKcal != nil {
			if len(m.Items) > 0 {
				return domain.InvalidState("The totals of a meal with items come from its items.")
			}
			if c.TotalCarbsG != nil {
				m.TotalCarbsG = *c.TotalCarbsG
			}
			if c.TotalKcal != nil {
				m.TotalCaloriesKcal = *c.TotalKcal
			}
		}
		return domain.ValidateMeal(*m, now)
	})
}

// ReplaceItems applies the full list of items (UC102).
func (d *Diary) ReplaceItems(ctx context.Context, a domain.Account, id uuid.UUID, items []domain.MealItemInput) (domain.Meal, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.Meal{}, err
	}
	if err := domain.ValidateItems("items", items, true); err != nil {
		return domain.Meal{}, err
	}
	return d.store.ReplaceMealItems(ctx, a.ID, pid, id, items)
}

// MarkEaten confirms a planned meal (UC101 A1).
func (d *Diary) MarkEaten(ctx context.Context, a domain.Account, id uuid.UUID, eatenAt *time.Time) (domain.Meal, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.Meal{}, err
	}
	now := d.now()
	at := now
	if eatenAt != nil {
		at = *eatenAt
	}
	if at.After(now.Add(domain.FutureTolerance)) || at.Before(domain.EarliestMeasurement) {
		return domain.Meal{}, domain.Invalid("eaten_at", "must be after 2020-01-01 and not in the future")
	}
	m, _, err := d.store.MarkMealEaten(ctx, pid, id, at)
	return m, err
}

func (d *Diary) CancelMeal(ctx context.Context, a domain.Account, id uuid.UUID) (domain.Meal, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.Meal{}, err
	}
	return d.store.CancelMeal(ctx, pid, id)
}

func (d *Diary) DeleteMeal(ctx context.Context, a domain.Account, id uuid.UUID) error {
	pid, err := patientOf(a)
	if err != nil {
		return err
	}
	return d.store.DeleteMeal(ctx, pid, id)
}

// ComputeImpacts is the job of UC108: the post-meal change of glucose of
// the meals eaten more than 2 hours ago.
func (d *Diary) ComputeImpacts(ctx context.Context) {
	meals, err := d.store.MealsDueForImpact(ctx, d.now(), 50)
	if err != nil {
		d.log.Warn("post-meal impact: could not read the meals", "error", err)
		return
	}
	for _, m := range meals {
		points, err := d.store.ReadingPoints(ctx, m.PatientID, m.EatenAt.Add(-15*time.Minute), m.EatenAt.Add(2*time.Hour+time.Second))
		if err != nil {
			d.log.Warn("post-meal impact: could not read the readings", "error", err)
			continue
		}
		var delta *int
		if v, ok := domain.Postprandial(points, *m.EatenAt); ok {
			delta = &v
		}
		if err := d.store.SetPostprandial(ctx, m.ID, delta); err != nil {
			d.log.Warn("post-meal impact: could not save it", "error", err)
		}
	}
}

// --- Meal photos (UC103–UC105) -------------------------------------------------

// Photo limits (contract and UC103).
const (
	MaxPhotoBytes    = 5 << 20
	maxPhotoSide     = 1600
	photoJPEGQuality = 85
)

// UploadPhoto stores a photo of the meal and queues its analysis. The image
// is decoded and saved again as JPEG of at most 1600 px, which also drops
// every metadata (location, date, phone) the app may have left (RNF06).
func (d *Diary) UploadPhoto(ctx context.Context, a domain.Account, mealID uuid.UUID, data []byte) (domain.MealPhoto, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.MealPhoto{}, err
	}
	ok, err := d.store.HasActiveConsent(ctx, a.ID, domain.DocExternalAIProcessing)
	if err != nil {
		return domain.MealPhoto{}, err
	}
	if !ok {
		return domain.MealPhoto{}, domain.ErrConsentRequired
	}
	if len(data) > MaxPhotoBytes {
		return domain.MealPhoto{}, domain.ErrPayloadTooLarge
	}
	clean, err := cleanImage(data)
	if err != nil {
		return domain.MealPhoto{}, err
	}
	p, err := d.store.CreateMealPhoto(ctx, pid, mealID, domain.StoredFile{Purpose: domain.FileMealPhoto, ContentType: "image/jpeg", Data: clean})
	if err == nil && d.analysis != nil {
		d.analysis.Wake()
	}
	return p, err
}

// cleanImage decodes a JPEG, PNG or WebP image, shrinks it to at most
// 1600 px and encodes it as JPEG without metadata.
func cleanImage(data []byte) ([]byte, error) {
	switch http.DetectContentType(data) {
	case "image/jpeg", "image/png", "image/webp":
	default:
		return nil, domain.ErrUnsupportedMediaType
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, domain.ErrUnsupportedMediaType
	}
	b := img.Bounds()
	if w, h := b.Dx(), b.Dy(); w > maxPhotoSide || h > maxPhotoSide {
		scale := float64(maxPhotoSide) / math.Max(float64(w), float64(h))
		dst := image.NewRGBA(image.Rect(0, 0, int(float64(w)*scale), int(float64(h)*scale)))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
		img = dst
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: photoJPEGQuality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (d *Diary) GetPhoto(ctx context.Context, a domain.Account, mealID, photoID uuid.UUID) (domain.MealPhoto, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.MealPhoto{}, err
	}
	return d.store.GetMealPhoto(ctx, pid, mealID, photoID)
}

// ConfirmPhoto adds the items the patient accepted (UC105).
func (d *Diary) ConfirmPhoto(ctx context.Context, a domain.Account, mealID, photoID uuid.UUID, items []domain.MealItemInput) (domain.Meal, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.Meal{}, err
	}
	if err := domain.ValidateItems("items", items, false); err != nil {
		return domain.Meal{}, err
	}
	return d.store.ConfirmMealPhoto(ctx, a.ID, pid, mealID, photoID, items)
}

// photoPrompt asks for the foods of the photo. The image carries no data
// about the patient (RNF06), and the answer is only a proposal (UC104).
const photoPrompt = `És um nutricionista português. Na fotografia está uma refeição.
Identifica cada alimento ou prato visível, com nomes simples em português de Portugal
(por exemplo "arroz branco cozido", "bife de vaca grelhado", "batata frita", "sopa de legumes").
Para cada um estima o peso em gramas, os hidratos de carbono em gramas (sem contar a fibra)
e as calorias. Dá também a tua confiança global na estimativa, de 0 a 100.
Se não houver comida na imagem, devolve a lista vazia e confiança 0.
Responde só com os dados pedidos: não dês conselhos, diagnósticos nem doses de insulina.`

var photoSchema = map[string]any{
	"type": "OBJECT",
	"properties": map[string]any{
		"items": map[string]any{
			"type": "ARRAY",
			"items": map[string]any{
				"type": "OBJECT",
				"properties": map[string]any{
					"name":          map[string]any{"type": "STRING"},
					"weight_g":      map[string]any{"type": "NUMBER"},
					"carbs_g":       map[string]any{"type": "NUMBER"},
					"calories_kcal": map[string]any{"type": "NUMBER"},
				},
				"required": []string{"name", "weight_g", "carbs_g", "calories_kcal"},
			},
		},
		"confidence_percent": map[string]any{"type": "NUMBER"},
	},
	"required": []string{"items", "confidence_percent"},
}

type photoAnswer struct {
	Items []struct {
		Name         string  `json:"name"`
		WeightG      float64 `json:"weight_g"`
		CarbsG       float64 `json:"carbs_g"`
		CaloriesKcal float64 `json:"calories_kcal"`
	} `json:"items"`
	Confidence float64 `json:"confidence_percent"`
}

// AnalyzePhotos is the worker of UC104: it analyses the pending photos and
// tells the patient's phone when each one is done.
func (d *Diary) AnalyzePhotos(ctx context.Context) {
	photos, err := d.store.ClaimPhotoAnalyses(ctx, d.now(), 5)
	if err != nil {
		d.log.Warn("photo analysis: could not read pending photos", "error", err)
		return
	}
	for _, p := range photos {
		status := d.analyze(ctx, p)
		if status == domain.PhotoPending {
			continue // tried again later
		}
		card, err := d.store.PatientCard(ctx, p.PatientID)
		if err != nil {
			continue
		}
		d.notifier.Notify(ctx, []uuid.UUID{card.UserID}, map[string]string{
			"type": "meal_photo_analyzed", "meal_id": p.MealID.String(), "photo_id": p.ID.String(), "analysis_status": status,
		})
	}
}

func (d *Diary) analyze(ctx context.Context, p domain.MealPhoto) string {
	fail := func(reason string, err error) string {
		if p.Attempts < domain.MaxPhotoAttempts && !errors.Is(err, gemini.ErrNotConfigured) {
			d.log.Warn("photo analysis failed, will retry", "reason", reason, "error", err)
			return domain.PhotoPending
		}
		d.log.Warn("photo analysis failed", "reason", reason, "error", err)
		if err := d.store.FailPhotoAnalysis(ctx, p.ID); err != nil {
			d.log.Warn("photo analysis: could not record the failure", "error", err)
		}
		return domain.PhotoFailed
	}
	if d.ai == nil || !d.ai.Configured() {
		return fail("no AI configured", gemini.ErrNotConfigured)
	}
	if p.FileID == nil {
		return fail("photo without image", errors.New("no file"))
	}
	file, err := d.store.GetFile(ctx, *p.FileID)
	if err != nil {
		return fail("image not found", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var answer photoAnswer
	model, err := d.ai.GenerateJSON(ctx, photoPrompt, &gemini.Image{MIMEType: file.ContentType, Data: file.Data}, photoSchema, &answer)
	if err != nil {
		return fail("model error", err)
	}
	result := domain.PhotoAnalysis{ModelVersion: model, Items: []domain.ProposedItem{}}
	var weight, carbs, kcal float64
	for _, it := range answer.Items {
		name := strings.TrimSpace(it.Name)
		if name == "" || it.WeightG <= 0 || it.WeightG > 10000 || it.CarbsG < 0 || it.CarbsG > 2000 || it.CaloriesKcal < 0 || it.CaloriesKcal > 15000 {
			continue
		}
		item := domain.ProposedItem{Name: name, EstimatedWeightG: ptrTo(domain.Round2(it.WeightG)),
			EstimatedCarbsG: ptrTo(domain.Round2(it.CarbsG)), EstimatedCaloriesKcal: ptrTo(domain.Round2(it.CaloriesKcal))}
		d.matchCatalogue(ctx, &item)
		result.Items = append(result.Items, item)
		weight, carbs, kcal = weight+it.WeightG, carbs+it.CarbsG, kcal+it.CaloriesKcal
	}
	if weight > 0 && weight <= 10000 && carbs <= 2000 && kcal <= 15000 {
		result.WeightG, result.CarbsG, result.CaloriesKcal = ptrTo(domain.Round2(weight)), ptrTo(domain.Round2(carbs)), ptrTo(domain.Round2(kcal))
	}
	conf := math.Max(0, math.Min(100, answer.Confidence))
	result.ConfidencePercent = ptrTo(domain.Round2(conf))
	if err := d.store.CompletePhotoAnalysis(ctx, p.ID, result); err != nil {
		return fail("could not save the analysis", err)
	}
	return domain.PhotoCompleted
}

// matchCatalogue finds the closest food of the catalogue and the number of
// servings that matches the estimated weight (UC104 step 5).
func (d *Diary) matchCatalogue(ctx context.Context, item *domain.ProposedItem) {
	// "arroz branco cozido" may only match "Arroz cozido simples": drop words
	// from the end until something matches.
	var foods []domain.Food
	words := strings.Fields(item.Name)
	for n := len(words); n >= 1 && len(foods) == 0; n-- {
		q := strings.Join(words[:n], " ")
		if utf8.RuneCountInString(q) < 3 {
			break
		}
		var err error
		if foods, err = d.store.SearchFoods(ctx, uuid.Nil, q, 5); err != nil {
			return
		}
	}
	for _, f := range foods {
		servings := append([]domain.FoodServing(nil), f.Servings...)
		sort.SliceStable(servings, func(i, j int) bool { // prefer 100 g, then any gram serving
			return gramScore(servings[i]) > gramScore(servings[j])
		})
		for _, s := range servings {
			if s.MetricUnit == nil || *s.MetricUnit != "g" || s.MetricAmount == nil || *s.MetricAmount <= 0 {
				continue
			}
			q := domain.Round2(*item.EstimatedWeightG / *s.MetricAmount)
			if q < domain.MinServingQuantity || q > domain.MaxServingQuantity {
				continue
			}
			fid, sid := f.ID, s.ID
			item.FoodID, item.ServingID, item.ServingQuantity = &fid, &sid, &q
			return
		}
	}
}

func gramScore(s domain.FoodServing) int {
	switch {
	case s.MetricUnit != nil && *s.MetricUnit == "g" && s.MetricAmount != nil && *s.MetricAmount == 100:
		return 2
	case s.MetricUnit != nil && *s.MetricUnit == "g":
		return 1
	}
	return 0
}

func ptrTo[T any](v T) *T { return &v }

// --- Insulin (UC109–UC111) ---------------------------------------------------------

func (d *Diary) CreateInsulin(ctx context.Context, a domain.Account, l domain.InsulinLog) (domain.InsulinLog, bool, error) {
	pid, err := patientOf(a)
	if err != nil {
		return l, false, err
	}
	l.PatientID, l.Brand, l.Notes = pid, orNil(l.Brand), orNil(l.Notes)
	l.DoseUnits = math.Round(l.DoseUnits*10) / 10
	if l.IdempotencyKey == nil {
		return l, false, domain.Invalid("idempotency_key", "is required")
	}
	if err := l.Validate(d.now()); err != nil {
		return l, false, err
	}
	return d.store.CreateInsulinLog(ctx, l)
}

func (d *Diary) UpdateInsulin(ctx context.Context, a domain.Account, id uuid.UUID, change func(*domain.InsulinLog)) (domain.InsulinLog, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.InsulinLog{}, err
	}
	now := d.now()
	return d.store.UpdateInsulinLog(ctx, pid, id, func(l *domain.InsulinLog) error {
		change(l)
		l.Brand, l.Notes = orNil(l.Brand), orNil(l.Notes)
		l.DoseUnits = math.Round(l.DoseUnits*10) / 10
		return l.Validate(now)
	})
}

func (d *Diary) DeleteInsulin(ctx context.Context, a domain.Account, id uuid.UUID) error {
	pid, err := patientOf(a)
	if err != nil {
		return err
	}
	return d.store.DeleteInsulinLog(ctx, pid, id)
}

func (d *Diary) ListInsulin(ctx context.Context, a domain.Account, from, to time.Time) ([]domain.InsulinLog, []domain.InsulinDailyTotal, error) {
	pid, err := patientOf(a)
	if err != nil {
		return nil, nil, err
	}
	if err := domain.CheckRange(from, to); err != nil {
		return nil, nil, err
	}
	return d.store.ListInsulinLogs(ctx, pid, from, to)
}

// --- Physical activity (UC089–UC091) ---------------------------------------------

func (d *Diary) CreateActivity(ctx context.Context, a domain.Account, act domain.Activity) (domain.Activity, bool, error) {
	pid, err := patientOf(a)
	if err != nil {
		return act, false, err
	}
	act.PatientID, act.Name = pid, strings.TrimSpace(act.Name)
	if act.IdempotencyKey == nil {
		return act, false, domain.Invalid("idempotency_key", "is required")
	}
	if err := act.Validate(d.now()); err != nil {
		return act, false, err
	}
	return d.store.CreateActivity(ctx, act)
}

// IngestActivities stores sessions imported from a watch (UC090).
func (d *Diary) IngestActivities(ctx context.Context, a domain.Account, in []domain.Activity) (domain.BatchResult, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.BatchResult{}, err
	}
	now := d.now()
	res := domain.BatchResult{Rejected: []domain.RejectedItem{}}
	var valid []domain.Activity
	var index []int
	for i, act := range in {
		act.PatientID, act.Name = pid, strings.TrimSpace(act.Name)
		key := ""
		if act.IdempotencyKey != nil {
			key = *act.IdempotencyKey
		}
		if err := act.Validate(now); err != nil || act.IdempotencyKey == nil {
			msg := "idempotency_key is required"
			var v *domain.ValidationError
			if errors.As(err, &v) {
				msg = v.Issues[0].Field + " " + v.Issues[0].Issue
			}
			res.Rejected = append(res.Rejected, rejected(i, key, domain.RejectValidation, msg))
			continue
		}
		valid, index = append(valid, act), append(index, i)
	}
	if len(valid) > 0 {
		outcomes, err := d.store.IngestActivities(ctx, pid, valid)
		if err != nil {
			return res, err
		}
		for k, o := range outcomes {
			switch o {
			case domain.OutcomeInserted:
				res.Inserted++
			case domain.OutcomeDuplicate:
				res.Duplicates++
			default:
				res.Rejected = append(res.Rejected, rejected(index[k], *valid[k].IdempotencyKey, domain.RejectUnknownDevice,
					"device_id is not one of the patient's devices"))
			}
		}
	}
	sort.Slice(res.Rejected, func(i, j int) bool { return res.Rejected[i].Index < res.Rejected[j].Index })
	return res, nil
}

func (d *Diary) UpdateActivity(ctx context.Context, a domain.Account, id uuid.UUID, change func(*domain.Activity)) (domain.Activity, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.Activity{}, err
	}
	now := d.now()
	return d.store.UpdateActivity(ctx, pid, id, func(act *domain.Activity) error {
		change(act)
		act.Name = strings.TrimSpace(act.Name)
		return act.Validate(now)
	})
}

func (d *Diary) DeleteActivity(ctx context.Context, a domain.Account, id uuid.UUID) error {
	pid, err := patientOf(a)
	if err != nil {
		return err
	}
	return d.store.DeleteActivity(ctx, pid, id)
}

func (d *Diary) ListActivities(ctx context.Context, a domain.Account, from, to time.Time) ([]domain.Activity, error) {
	pid, err := patientOf(a)
	if err != nil {
		return nil, err
	}
	if err := domain.CheckRange(from, to); err != nil {
		return nil, err
	}
	return d.store.ListActivities(ctx, pid, from, to)
}
