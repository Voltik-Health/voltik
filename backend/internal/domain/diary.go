package domain

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Food types and data sources (foods).
const (
	FoodGeneric       = "generic"
	FoodBrand         = "brand"
	FoodCompositeDish = "composite_dish"

	SourceINSA                  = "insa"
	SourceOpenFoodFacts         = "open_food_facts"
	SourcePortugueseTraditional = "portuguese_traditional"
	SourceUserCustom            = "user_custom"
	SourceFatSecret             = "fatsecret"
)

// AttributionINSA and AttributionOpenFoodFacts must appear wherever the data
// of those sources is shown (their terms of use).
const (
	AttributionINSA          = "Fonte: Base de Dados da Composição de Alimentos. Instituto Nacional de Saúde Doutor Ricardo Jorge, I. P. - INSA. v 7.1 - 2026"
	AttributionOpenFoodFacts = "Fonte: Open Food Facts (openfoodfacts.org), licença ODbL"
)

// Attribution is the source text to show next to a food, if its source needs one.
func Attribution(dataSource string) *string {
	switch dataSource {
	case SourceINSA:
		return ptrTo(AttributionINSA)
	case SourceOpenFoodFacts:
		return ptrTo(AttributionOpenFoodFacts)
	}
	return nil
}

// Food is a row of foods with its servings and, for a dish, its ingredients.
type Food struct {
	ID          uuid.UUID
	Name        string
	Brand       *string
	Type        string
	DataSource  string
	Category    *FoodCategoryRef
	BarcodeEAN  *string
	IsVerified  bool
	CreatedBy   *uuid.UUID
	Servings    []FoodServing
	Components  []FoodComponent
	CreatedAt   time.Time
	UpdatedAt   time.Time
	UsageCount  int // meal items that use the food (review queue only)
	FatSecretID *int64
}

type FoodCategoryRef struct {
	ID   uuid.UUID
	Name string
}

// FoodCategory is a row of food_categories with the number of visible foods.
type FoodCategory struct {
	ID        uuid.UUID
	Name      string
	FoodCount int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Nutrition of a serving. Carbohydrates exclude fibre (EU convention).
type Nutrition struct {
	CarbsG        float64
	FiberG        *float64
	SugarsG       *float64
	CaloriesKcal  float64
	ProteinG      float64
	FatG          float64
	SaturatedFatG *float64
	SodiumMg      *float64
}

// FoodServing is a row of food_servings.
type FoodServing struct {
	ID           uuid.UUID
	FoodID       uuid.UUID
	Description  string
	MetricAmount *float64
	MetricUnit   *string
	Nutrition
}

// FoodComponent is an ingredient of a composite dish.
type FoodComponent struct {
	ID              uuid.UUID
	ComponentFoodID uuid.UUID
	FoodName        string
	DefaultGrams    float64
}

var barcodePattern = regexp.MustCompile(`^[0-9]{8,14}$`)

// ValidBarcode reports whether the text is an EAN-8, EAN-13 or UPC code.
func ValidBarcode(code string) bool { return barcodePattern.MatchString(code) }

// Limits of the food tables.
const (
	maxFoodNameLength    = 200
	maxBrandLength       = 100
	maxCategoryLength    = 80
	maxServingDescLength = 100
	maxServingsPerFood   = 20
)

// ValidateFood checks a food and its servings before it is stored.
func ValidateFood(f Food, requireServing bool) error {
	var v ValidationError
	checkText(&v, "food_name", f.Name, maxFoodNameLength)
	if f.Brand != nil {
		checkText(&v, "brand", *f.Brand, maxBrandLength)
	}
	if f.BarcodeEAN != nil && !ValidBarcode(*f.BarcodeEAN) {
		v.Add("barcode_ean", "must have 8 to 14 digits")
	}
	switch {
	case requireServing && len(f.Servings) == 0:
		v.Add("servings", "must have at least one serving")
	case len(f.Servings) > maxServingsPerFood:
		v.Add("servings", fmt.Sprintf("must have at most %d servings", maxServingsPerFood))
	}
	seen := map[string]bool{}
	for i, s := range f.Servings {
		s.validate(&v, fmt.Sprintf("servings[%d].", i))
		key := strings.ToLower(strings.TrimSpace(s.Description))
		if seen[key] {
			v.Add(fmt.Sprintf("servings[%d].serving_description", i), "is repeated")
		}
		seen[key] = true
	}
	return v.OrNil()
}

// ValidateServing checks one serving.
func ValidateServing(s FoodServing) error {
	var v ValidationError
	s.validate(&v, "")
	return v.OrNil()
}

func (s FoodServing) validate(v *ValidationError, prefix string) {
	checkText(v, prefix+"serving_description", s.Description, maxServingDescLength)
	if s.MetricAmount != nil && (*s.MetricAmount < 0.01 || *s.MetricAmount > 50000) {
		v.Add(prefix+"metric_amount", "must be between 0.01 and 50000")
	}
	if s.MetricUnit != nil && *s.MetricUnit != "g" && *s.MetricUnit != "ml" {
		v.Add(prefix+"metric_unit", "must be g or ml")
	}
	inRange := func(field string, value *float64, max float64) {
		if value != nil && (*value < 0 || *value > max) {
			v.Add(prefix+field, fmt.Sprintf("must be between 0 and %g", max))
		}
	}
	inRange("carbs_g", &s.CarbsG, 1000)
	inRange("fiber_g", s.FiberG, 1000)
	inRange("sugars_g", s.SugarsG, 1000)
	inRange("calories_kcal", &s.CaloriesKcal, 9000)
	inRange("protein_g", &s.ProteinG, 1000)
	inRange("fat_g", &s.FatG, 1000)
	inRange("saturated_fat_g", s.SaturatedFatG, 1000)
	inRange("sodium_mg", s.SodiumMg, 50000)
	if s.SugarsG != nil && *s.SugarsG > s.CarbsG {
		v.Add(prefix+"sugars_g", "must not exceed carbs_g")
	}
	if s.SaturatedFatG != nil && *s.SaturatedFatG > s.FatG {
		v.Add(prefix+"saturated_fat_g", "must not exceed fat_g")
	}
}

// ValidateCategoryName checks the name of a food category.
func ValidateCategoryName(name string) error {
	var v ValidationError
	checkText(&v, "name", name, maxCategoryLength)
	return v.OrNil()
}

// Meal statuses and types.
const (
	MealPlanned   = "planned"
	MealEaten     = "eaten"
	MealCancelled = "cancelled"
)

// MealTypes are the codes of meal_types.
var MealTypes = []string{"breakfast", "morning_snack", "lunch", "afternoon_snack", "dinner", "evening_snack", "other"}

// Meal limits (000002).
const (
	MaxMealCarbsG       = 2000
	MaxMealCaloriesKcal = 15000
	MaxMealItems        = 50
	MinServingQuantity  = 0.01
	MaxServingQuantity  = 100
	maxMealNotesLength  = 1000
)

// Meal is a row of meals with its items, photos and linked insulin doses.
type Meal struct {
	ID                     uuid.UUID
	PatientID              uuid.UUID
	MealType               string
	MealDate               time.Time
	PlannedTime            *string // HH:MM in Portugal
	Status                 string
	EatenAt                *time.Time
	TotalCarbsG            float64
	TotalCaloriesKcal      float64
	PostprandialDeltaMgDl  *int
	Notes                  *string
	IdempotencyKey         *string
	Items                  []MealItem
	Photos                 []MealPhoto
	InsulinLogs            []InsulinLog
	CreatedAt              time.Time
	UpdatedAt              time.Time
	PostprandialComputedAt *time.Time
}

// MealItem is a row of meal_items, with the names from the catalogue.
type MealItem struct {
	ID                 uuid.UUID
	MealID             uuid.UUID
	FoodID             uuid.UUID
	FoodName           string
	ServingID          uuid.UUID
	ServingDescription string
	Quantity           float64
	CarbsG             float64
	CaloriesKcal       float64
	IsEaten            bool
	CreatedAt          time.Time
}

// MealItemInput is an item sent by the app.
type MealItemInput struct {
	ID        *uuid.UUID
	ServingID uuid.UUID
	Quantity  float64
	IsEaten   bool
}

// ValidateItems checks the items of a meal. prefix names the list, e.g. "items".
func ValidateItems(prefix string, items []MealItemInput, allowIDs bool) error {
	var v ValidationError
	if len(items) > MaxMealItems {
		v.Add(prefix, fmt.Sprintf("must have at most %d items", MaxMealItems))
	}
	ids := map[uuid.UUID]bool{}
	for i, it := range items {
		field := fmt.Sprintf("%s[%d].", prefix, i)
		if it.Quantity < MinServingQuantity || it.Quantity > MaxServingQuantity {
			v.Add(field+"serving_quantity", "must be between 0.01 and 100")
		}
		if it.ID != nil {
			switch {
			case !allowIDs:
				v.Add(field+"id", "must be left out")
			case ids[*it.ID]:
				v.Add(field+"id", "is repeated")
			}
			ids[*it.ID] = true
		}
	}
	return v.OrNil()
}

var plannedTimePattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// ValidateMeal checks the fields of a meal (not its items).
func ValidateMeal(m Meal, now time.Time) error {
	var v ValidationError
	if !validMealType(m.MealType) {
		v.Add("meal_type", "must be one of the listed values")
	}
	if m.PlannedTime != nil && !plannedTimePattern.MatchString(*m.PlannedTime) {
		v.Add("planned_time", "must be HH:MM")
	}
	if m.Status == MealEaten {
		switch {
		case m.EatenAt == nil:
			v.Add("eaten_at", "is required for an eaten meal")
		case m.EatenAt.After(now.Add(FutureTolerance)):
			v.Add("eaten_at", "must not be in the future")
		case m.EatenAt.Before(EarliestMeasurement):
			v.Add("eaten_at", "must be after 2020-01-01")
		}
	}
	if m.Notes != nil && utf8.RuneCountInString(*m.Notes) > maxMealNotesLength {
		v.Add("notes", "must be at most 1000 characters")
	}
	if m.TotalCarbsG < 0 || m.TotalCarbsG > MaxMealCarbsG {
		v.Add("total_carbs_g", "must be between 0 and 2000")
	}
	if m.TotalCaloriesKcal < 0 || m.TotalCaloriesKcal > MaxMealCaloriesKcal {
		v.Add("total_calories_kcal", "must be between 0 and 15000")
	}
	return v.OrNil()
}

func validMealType(t string) bool {
	for _, mt := range MealTypes {
		if t == mt {
			return true
		}
	}
	return false
}

// Round2 rounds to two decimal places, like the DECIMAL(…,2) columns.
func Round2(v float64) float64 { return math.Round(v*100) / 100 }

// Photo analysis states.
const (
	PhotoPending   = "pending"
	PhotoCompleted = "completed"
	PhotoFailed    = "failed"

	// The proposal is flagged below this confidence (UC104 A2).
	LowConfidencePercent = 60
	// A photo is analysed at most this many times (UC104).
	MaxPhotoAttempts = 3
)

// MealPhoto is a row of meal_photos.
type MealPhoto struct {
	ID                    uuid.UUID
	MealID                uuid.UUID
	PatientID             uuid.UUID
	FileID                *uuid.UUID
	AnalysisStatus        string
	EstimatedWeightG      *float64
	EstimatedCarbsG       *float64
	EstimatedCaloriesKcal *float64
	ConfidencePercent     *float64
	ProposedItems         []ProposedItem
	ModelVersion          *string
	AnalyzedAt            *time.Time
	ConfirmedAt           *time.Time
	Attempts              int
	CreatedAt             time.Time
}

// ProposedItem is a food the model recognised in a photo.
type ProposedItem struct {
	Name                  string     `json:"name"`
	EstimatedWeightG      *float64   `json:"estimated_weight_g"`
	EstimatedCarbsG       *float64   `json:"estimated_carbs_g"`
	EstimatedCaloriesKcal *float64   `json:"estimated_calories_kcal"`
	FoodID                *uuid.UUID `json:"food_id"`
	ServingID             *uuid.UUID `json:"serving_id"`
	ServingQuantity       *float64   `json:"serving_quantity"`
}

// PhotoAnalysis is the result of analysing a photo.
type PhotoAnalysis struct {
	Items             []ProposedItem
	WeightG           *float64
	CarbsG            *float64
	CaloriesKcal      *float64
	ConfidencePercent *float64
	ModelVersion      string
}

// Insulin types and reasons.
var (
	InsulinTypes   = []string{"rapid_bolus", "ultra_rapid_bolus", "basal", "premixed"}
	InsulinReasons = []string{"meal", "correction", "scheduled_basal", "mixed"}
)

// InsulinLog is a row of insulin_logs.
type InsulinLog struct {
	ID             uuid.UUID
	PatientID      uuid.UUID
	InsulinType    string
	Brand          *string
	DoseUnits      float64
	AdministeredAt time.Time
	Reason         string
	MealID         *uuid.UUID
	Notes          *string
	IdempotencyKey *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// EditedAfter24h reports whether the dose was corrected more than a day after it was taken (UC110).
func (l InsulinLog) EditedAfter24h() bool {
	return l.UpdatedAt.Sub(l.CreatedAt) > time.Second && l.UpdatedAt.Sub(l.AdministeredAt) > 24*time.Hour
}

// Validate checks an insulin dose.
func (l InsulinLog) Validate(now time.Time) error {
	var v ValidationError
	if !oneOf(l.InsulinType, InsulinTypes) {
		v.Add("insulin_type", "must be one of the listed values")
	}
	if !oneOf(l.Reason, InsulinReasons) {
		v.Add("reason", "must be one of the listed values")
	}
	if l.DoseUnits < 0.1 || l.DoseUnits > 100 {
		v.Add("dose_units", "must be between 0.1 and 100")
	}
	if l.Brand != nil {
		checkText(&v, "insulin_brand", *l.Brand, 80)
	}
	if l.Notes != nil && utf8.RuneCountInString(*l.Notes) > 255 {
		v.Add("notes", "must be at most 255 characters")
	}
	switch {
	case l.AdministeredAt.After(now.Add(FutureTolerance)):
		v.Add("administered_at", "must not be in the future")
	case l.AdministeredAt.Before(EarliestMeasurement):
		v.Add("administered_at", "must be after 2020-01-01")
	}
	if l.IdempotencyKey != nil {
		checkText(&v, "idempotency_key", *l.IdempotencyKey, maxKeyLength)
	}
	return v.OrNil()
}

// InsulinDailyTotal is the sum of one insulin type on one day.
type InsulinDailyTotal struct {
	Date        time.Time
	InsulinType string
	TotalUnits  float64
	Doses       int
}

// Activity intensities.
var ActivityIntensities = []string{"light", "moderate", "vigorous"}

// Activity is a row of physical_activity_logs.
type Activity struct {
	ID              uuid.UUID
	PatientID       uuid.UUID
	Name            string
	StartedAt       time.Time
	DurationMinutes int
	Intensity       string
	TotalSteps      *int
	AvgHeartRateBPM *int
	CaloriesKcal    *int
	DeviceID        *uuid.UUID
	IdempotencyKey  *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Validate checks an activity (UC089).
func (a Activity) Validate(now time.Time) error {
	var v ValidationError
	checkText(&v, "activity_name", a.Name, 100)
	if a.DurationMinutes < 1 || a.DurationMinutes > 1440 {
		v.Add("duration_minutes", "must be between 1 and 1440")
	}
	if !oneOf(a.Intensity, ActivityIntensities) {
		v.Add("intensity", "must be one of the listed values")
	}
	if a.TotalSteps != nil && (*a.TotalSteps < 0 || *a.TotalSteps > 200000) {
		v.Add("total_steps", "must be between 0 and 200000")
	}
	if a.AvgHeartRateBPM != nil && (*a.AvgHeartRateBPM < 30 || *a.AvgHeartRateBPM > 250) {
		v.Add("avg_heart_rate_bpm", "must be between 30 and 250")
	}
	if a.CaloriesKcal != nil && (*a.CaloriesKcal < 0 || *a.CaloriesKcal > 15000) {
		v.Add("calories_burned_kcal", "must be between 0 and 15000")
	}
	switch {
	case a.StartedAt.After(now.Add(FutureTolerance)):
		v.Add("started_at", "must not be in the future")
	case a.StartedAt.Before(EarliestMeasurement):
		v.Add("started_at", "must be after 2020-01-01")
	}
	if a.IdempotencyKey != nil {
		checkText(&v, "idempotency_key", *a.IdempotencyKey, maxKeyLength)
	}
	return v.OrNil()
}

func oneOf(v string, values []string) bool {
	for _, x := range values {
		if v == x {
			return true
		}
	}
	return false
}
