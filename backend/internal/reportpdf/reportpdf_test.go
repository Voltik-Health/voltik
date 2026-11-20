package reportpdf

import (
	"bytes"
	"testing"
	"time"

	"github.com/voltik/voltik/backend/internal/domain"
)

func TestRender(t *testing.T) {
	start := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	var points []domain.Point
	for i := 0; i < 7*288; i++ {
		at := start.Add(time.Duration(i) * 5 * time.Minute)
		points = append(points, domain.Point{At: at, Value: 120 + (i%288)/3})
	}
	min, max, cov := 70, 180, 64.5
	sd, cv, gmi := 30.2, 22.1, 6.8
	summary := "Esta semana passou 72 % do tempo no intervalo alvo."
	pdf, err := Render(Input{
		PatientName: "Manuel Sousa", BirthDate: time.Date(1968, 3, 2, 0, 0, 0, 0, time.UTC), GeneratedAt: start.AddDate(0, 0, 8),
		Points: points,
		Report: domain.Report{
			PeriodType: domain.PeriodWeek, StartDate: start, EndDate: start.AddDate(0, 0, 6), GeneratedByRole: domain.GeneratedByPatient,
			TargetMin: &min, TargetMax: &max, CoveragePercent: &cov, TimeInRange: 72.4, TimeBelowRange: 3.1, TimeAboveRange: 24.5,
			MeanMgDl: 152, StdDevMgDl: &sd, CoefficientOfVar: &cv, GMIPercent: &gmi, IrisSummary: &summary,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || len(pdf) < 2000 {
		t.Fatalf("not a PDF (%d bytes)", len(pdf))
	}
}

func TestAGP(t *testing.T) {
	day := time.Date(2026, 1, 12, 0, 0, 0, 0, domain.Lisbon)
	var points []domain.Point
	for d := 0; d < 5; d++ {
		points = append(points, domain.Point{At: day.AddDate(0, 0, d).Add(8 * time.Hour), Value: 100 + d*10})
	}
	bins := AGP(points)
	if len(bins) != 1 || bins[0].Minute != 8*60 || bins[0].P50 != 120 || bins[0].P5 != 102 || bins[0].P95 != 138 {
		t.Fatalf("bins = %+v", bins)
	}
	if len(AGP(points[:2])) != 0 {
		t.Fatal("bins with fewer than 3 readings are left out")
	}
}
