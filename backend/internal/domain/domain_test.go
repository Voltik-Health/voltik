package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var today = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)

func validPatient() Patient {
	return Patient{
		BirthDate:    time.Date(1968, 3, 2, 0, 0, 0, 0, time.UTC),
		DiabetesType: "type_1",
		Thresholds:   DefaultThresholds,
	}
}

func ptr[T any](v T) *T { return &v }

// fields returns the fields of a validation error, or nil when err is nil.
func fields(t *testing.T, err error) []string {
	t.Helper()
	if err == nil {
		return nil
	}
	var v *ValidationError
	if !errors.As(err, &v) {
		t.Fatalf("not a validation error: %v", err)
	}
	var out []string
	for _, i := range v.Issues {
		out = append(out, i.Field)
	}
	return out
}

func TestPatientValidate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Patient)
		want   []string
	}{
		{"defaults are valid", func(*Patient) {}, nil},
		{"born today", func(p *Patient) { p.BirthDate = today }, nil},
		{"born tomorrow", func(p *Patient) { p.BirthDate = today.AddDate(0, 0, 1) }, []string{"birth_date"}},
		{"born before 1900", func(p *Patient) { p.BirthDate = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC) }, []string{"birth_date"}},
		{"no diabetes type", func(p *Patient) { p.DiabetesType = "" }, []string{"diabetes_type"}},
		{"blank occupation", func(p *Patient) { p.Occupation = ptr("  ") }, []string{"occupation"}},
		{"long occupation", func(p *Patient) { p.Occupation = ptr(strings.Repeat("a", 101)) }, []string{"occupation"}},
		{"weight limits", func(p *Patient) { p.WeightKg = ptr(2.0) }, nil},
		{"weight too low", func(p *Patient) { p.WeightKg = ptr(1.99) }, []string{"weight_kg"}},
		{"weight too high", func(p *Patient) { p.WeightKg = ptr(500.01) }, []string{"weight_kg"}},
		{"height too low", func(p *Patient) { p.HeightCm = ptr(29) }, []string{"height_cm"}},
		{"height too high", func(p *Patient) { p.HeightCm = ptr(261) }, []string{"height_cm"}},
		{"target min out of range", func(p *Patient) { p.TargetMin = 39 }, []string{"target_min_mg_dl"}},
		{"critical high out of range", func(p *Patient) { p.CriticalHigh = 551 }, []string{"critical_high_mg_dl"}},
		{"target min not below target max", func(p *Patient) { p.TargetMin = 180 },
			[]string{"target_min_mg_dl", "target_max_mg_dl"}},
		{"critical low not below target min", func(p *Patient) { p.CriticalLow = 70 },
			[]string{"critical_low_mg_dl", "target_min_mg_dl"}},
		{"critical high not above target max", func(p *Patient) { p.CriticalHigh = 150 },
			[]string{"target_max_mg_dl", "critical_high_mg_dl"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validPatient()
			tt.change(&p)
			got := fields(t, p.Validate(today))
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("fields = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUserValidateProfile(t *testing.T) {
	ok := User{FullName: "Manuel Sousa", Phone: ptr("+351912345678")}
	if err := ok.ValidateProfile(); err != nil {
		t.Fatalf("valid profile rejected: %v", err)
	}
	bad := User{FullName: " ", Phone: ptr(strings.Repeat("9", 31))}
	if got := fields(t, bad.ValidateProfile()); strings.Join(got, ",") != "full_name,phone" {
		t.Fatalf("fields = %v", got)
	}
	long := User{FullName: strings.Repeat("é", 151)}
	if got := fields(t, long.ValidateProfile()); strings.Join(got, ",") != "full_name" {
		t.Fatalf("fields = %v", got)
	}
}

func TestDocumentsInForce(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	docs := []LegalDocument{
		{Type: DocTermsOfService, Version: "1.0", PublishedAt: now.AddDate(0, -2, 0)},
		{Type: DocTermsOfService, Version: "1.1", PublishedAt: now.AddDate(0, -1, 0)},
		{Type: DocTermsOfService, Version: "2.0", PublishedAt: now.AddDate(0, 0, 7)}, // not yet in force
		{Type: DocBetaTesting, Version: "1.0", PublishedAt: now.AddDate(0, -2, 0)},
	}
	got := DocumentsInForce(docs, now)
	if got[DocTermsOfService].Version != "1.1" || got[DocBetaTesting].Version != "1.0" || len(got) != 2 {
		t.Fatalf("in force = %+v", got)
	}
}

func TestAccountHasRole(t *testing.T) {
	a := Account{Roles: []Role{RolePatient, RoleCaregiver}}
	if !a.HasRole(RoleCaregiver) || a.HasRole(RoleAdmin) {
		t.Fatalf("HasRole wrong for %v", a.Roles)
	}
}

func TestToday(t *testing.T) {
	lisbon := time.FixedZone("WEST", 3600)
	got := Today(time.Date(2026, 11, 3, 0, 30, 0, 0, lisbon)) // 23:30 UTC the day before
	if !got.Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("Today = %v", got)
	}
}
