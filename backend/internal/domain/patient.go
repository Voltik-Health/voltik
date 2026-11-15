package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Thresholds are the patient's glucose limits, in mg/dL.
type Thresholds struct {
	TargetMin    int
	TargetMax    int
	CriticalLow  int
	CriticalHigh int
}

// DefaultThresholds apply when onboarding leaves them out.
var DefaultThresholds = Thresholds{TargetMin: 70, TargetMax: 180, CriticalLow: 55, CriticalHigh: 250}

// Patient is a row of patients. The enumerations are their codes
// (diabetes_types.code, ...), never their ids.
type Patient struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	BirthDate    time.Time
	Gender       *string
	Occupation   *string
	DiabetesType string
	Therapy      *string
	Thresholds
	WeightKg  *float64
	HeightCm  *int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Limits of the patients table (migration 000002).
var minBirthDate = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)

const (
	maxOccupationLength = 100
	minWeightKg         = 2.0
	maxWeightKg         = 500.0
	minHeightCm         = 30
	maxHeightCm         = 260
	minGlucose          = 20
	minTargetMin        = 40
	maxGlucose          = 550
)

// Validate checks the rules of a patient profile. today is the current UTC date.
func (p Patient) Validate(today time.Time) error {
	var v ValidationError

	if p.BirthDate.Before(minBirthDate) || p.BirthDate.After(today) {
		v.Add("birth_date", "must be between 1900-01-01 and today")
	}
	if p.DiabetesType == "" {
		v.Add("diabetes_type", "is required")
	}
	if p.Occupation != nil {
		checkText(&v, "occupation", *p.Occupation, maxOccupationLength)
	}
	if p.WeightKg != nil && (*p.WeightKg < minWeightKg || *p.WeightKg > maxWeightKg) {
		v.Add("weight_kg", "must be between 2 and 500")
	}
	if p.HeightCm != nil && (*p.HeightCm < minHeightCm || *p.HeightCm > maxHeightCm) {
		v.Add("height_cm", "must be between 30 and 260")
	}
	p.Thresholds.validate(&v)
	return v.OrNil()
}

func (t Thresholds) validate(v *ValidationError) {
	inRange := func(field string, value, low int) bool {
		if value < low || value > maxGlucose {
			v.Add(field, fmt.Sprintf("must be between %d and %d", low, maxGlucose))
			return false
		}
		return true
	}
	ok := inRange("critical_low_mg_dl", t.CriticalLow, minGlucose)
	ok = inRange("target_min_mg_dl", t.TargetMin, minTargetMin) && ok
	ok = inRange("target_max_mg_dl", t.TargetMax, minGlucose) && ok
	ok = inRange("critical_high_mg_dl", t.CriticalHigh, minGlucose) && ok
	if !ok {
		return
	}

	// critical_low < target_min < target_max < critical_high
	order := []struct {
		field string
		value int
	}{
		{"critical_low_mg_dl", t.CriticalLow},
		{"target_min_mg_dl", t.TargetMin},
		{"target_max_mg_dl", t.TargetMax},
		{"critical_high_mg_dl", t.CriticalHigh},
	}
	for i := 0; i+1 < len(order); i++ {
		low, high := order[i], order[i+1]
		if low.value >= high.value {
			v.Add(low.field, "must be lower than "+high.field)
			v.Add(high.field, "must be higher than "+low.field)
		}
	}
}

// checkText applies the rules every text column has: not blank and not too long.
func checkText(v *ValidationError, field, value string, maxLength int) {
	switch {
	case strings.TrimSpace(value) == "":
		v.Add(field, "must not be blank")
	case utf8.RuneCountInString(value) > maxLength:
		v.Add(field, fmt.Sprintf("must be at most %d characters", maxLength))
	}
}

// Today is the current date in UTC, the time zone of the database.
func Today(now time.Time) time.Time {
	y, m, d := now.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
