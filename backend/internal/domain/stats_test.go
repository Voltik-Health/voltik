package domain

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
)

var t0 = time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)

// every5 returns readings every 5 minutes from t0 with the given values.
func every5(values ...int) []Point {
	out := make([]Point, len(values))
	for i, v := range values {
		out[i] = Point{At: t0.Add(time.Duration(i) * 5 * time.Minute), Value: v}
	}
	return out
}

func repeat(v, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestComputeStatsSplitsTheTimeAndAddsUpTo100(t *testing.T) {
	// 1 hour: 6 readings below 70, 3 above 180, 3 in range
	values := append(append(repeat(60, 6), repeat(200, 3)...), repeat(120, 3)...)
	s := ComputeStats(every5(values...), t0, t0.Add(time.Hour), DefaultThresholds, 0)
	if *s.TimeBelowRange != 50 || *s.TimeAboveRange != 25 || *s.TimeInRange != 25 {
		t.Fatalf("TBR/TAR/TIR = %v/%v/%v, want 50/25/25", *s.TimeBelowRange, *s.TimeAboveRange, *s.TimeInRange)
	}
	if s.ReadingsCount != 12 || s.CoveragePercent != 100 || s.LowCoverage {
		t.Fatalf("count %d, coverage %v", s.ReadingsCount, s.CoveragePercent)
	}
	if s.HypoEpisodes != 1 || s.HyperEpisodes != 1 {
		t.Fatalf("episodes hypo %d hyper %d, want 1 and 1", s.HypoEpisodes, s.HyperEpisodes)
	}
}

func TestComputeStatsMeanVariabilityAndGMI(t *testing.T) {
	s := ComputeStats(every5(100, 200, 100, 200), t0, t0.Add(20*time.Minute), DefaultThresholds, 45)
	if *s.MeanMgDl != 150 || *s.StdDevMgDl != 50 {
		t.Fatalf("mean %d sd %v", *s.MeanMgDl, *s.StdDevMgDl)
	}
	if *s.CoefficientOfVar != 33.3 {
		t.Fatalf("CV = %v, want 33.3", *s.CoefficientOfVar)
	}
	if want := math.Round((3.31+0.02392*150)*100) / 100; *s.GMIPercent != want {
		t.Fatalf("GMI = %v, want %v", *s.GMIPercent, want)
	}
	if s.ActiveMinutes != 45 {
		t.Fatalf("active minutes = %d", s.ActiveMinutes)
	}
}

func TestComputeStatsUsesThePatientsRange(t *testing.T) {
	limits := Thresholds{TargetMin: 80, TargetMax: 140, CriticalLow: 60, CriticalHigh: 250}
	s := ComputeStats(every5(75, 100, 150, 100), t0, t0.Add(20*time.Minute), limits, 0)
	if s.TargetMin != 80 || s.TargetMax != 140 || *s.TimeBelowRange != 25 || *s.TimeAboveRange != 25 {
		t.Fatalf("stats with the patient's limits: %+v", s)
	}
}

func TestComputeStatsWithoutReadings(t *testing.T) {
	s := ComputeStats(nil, t0, t0.Add(24*time.Hour), DefaultThresholds, 0)
	if s.TimeInRange != nil || s.MeanMgDl != nil || s.ReadingsCount != 0 || s.CoveragePercent != 0 || !s.LowCoverage {
		t.Fatalf("stats without data: %+v", s)
	}
}

func TestComputeStatsWeighsDenseReadingsByTime(t *testing.T) {
	// Juggluco sends one reading a minute: 10 low readings in 10 minutes
	// weigh as much as 2 readings of a 5-minute CGM.
	var points []Point
	for i := 0; i < 10; i++ {
		points = append(points, Point{At: t0.Add(time.Duration(i) * time.Minute), Value: 60})
	}
	points = append(points, Point{At: t0.Add(10 * time.Minute), Value: 120}, Point{At: t0.Add(15 * time.Minute), Value: 120})
	s := ComputeStats(points, t0, t0.Add(20*time.Minute), DefaultThresholds, 0)
	if *s.TimeBelowRange != 50 {
		t.Fatalf("TBR = %v, want 50 (10 of 20 minutes)", *s.TimeBelowRange)
	}
}

func TestEpisodesNeedFifteenMinutes(t *testing.T) {
	tests := []struct {
		name   string
		values []int
		want   int
	}{
		{"10 minutes below is not an episode", []int{120, 60, 60, 120}, 0},
		{"15 minutes below is one", []int{120, 60, 60, 60, 120}, 1},
		{"two separate runs", []int{60, 60, 60, 120, 120, 60, 60, 60}, 2},
		{"never below", []int{120, 130, 140}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := episodes(every5(tt.values...), func(v int) bool { return v < 70 }); got != tt.want {
				t.Fatalf("episodes = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEpisodeEndsWhenDataStops(t *testing.T) {
	// 10 minutes below, an hour without data, 10 minutes below again: two
	// short runs, not one long episode.
	points := append(every5(60, 60), Point{At: t0.Add(70 * time.Minute), Value: 60}, Point{At: t0.Add(75 * time.Minute), Value: 60})
	if got := episodes(points, func(v int) bool { return v < 70 }); got != 0 {
		t.Fatalf("episodes = %d, want 0", got)
	}
}

func TestCoverageCountsFiveMinuteSlots(t *testing.T) {
	// 1 day = 288 slots; readings in the first 72 slots = 25 %
	s := ComputeStats(every5(repeat(120, 72)...), t0, t0.Add(24*time.Hour), DefaultThresholds, 0)
	if s.CoveragePercent != 25 || !s.LowCoverage {
		t.Fatalf("coverage = %v, low = %v", s.CoveragePercent, s.LowCoverage)
	}
}

func TestPostprandial(t *testing.T) {
	eaten := t0.Add(30 * time.Minute)
	at := func(minutes, v int) Point { return Point{At: t0.Add(time.Duration(minutes) * time.Minute), Value: v} }
	points := []Point{at(20, 110), at(28, 115), at(40, 140), at(55, 180), at(70, 190), at(85, 170), at(100, 150), at(115, 130)}
	if d, ok := Postprandial(points, eaten); !ok || d != 75 {
		t.Fatalf("delta = %d (%v), want +75", d, ok)
	}
	// a meal followed by a drop
	down := []Point{at(25, 180), at(40, 150), at(55, 120), at(70, 95), at(85, 100), at(100, 110), at(115, 120)}
	if d, ok := Postprandial(down, eaten); !ok || d != -85 {
		t.Fatalf("delta = %d (%v), want -85", d, ok)
	}
	if _, ok := Postprandial(points[:4], eaten); ok {
		t.Fatal("fewer than 6 readings after the meal must give no value")
	}
	if _, ok := Postprandial([]Point{at(0, 100), at(40, 1), at(50, 1), at(60, 1), at(70, 1), at(80, 1), at(90, 1)}, eaten); ok {
		t.Fatal("without a reading in the 15 minutes before the meal there is no baseline")
	}
}

func TestCriticalAlertForReadings(t *testing.T) {
	r := GlucoseReading{ID: 7, MeasuredAt: t0, Trend: TrendFalling}
	tests := []struct {
		value int
		want  string
	}{{55, AlertCriticalLow}, {54, AlertCriticalLow}, {56, ""}, {249, ""}, {250, AlertCriticalHigh}, {400, AlertCriticalHigh}}
	for _, tt := range tests {
		r.ValueMgDl = tt.value
		a, ok := CriticalAlertFor(r, DefaultThresholds)
		if (tt.want == "") == ok || (ok && a.Type != tt.want) {
			t.Fatalf("value %d: alert %q (%v), want %q", tt.value, a.Type, ok, tt.want)
		}
		if ok && (a.Severity != SeverityCritical || !a.Escalate || *a.SourceReadingID != 7 || a.SuggestedAction == nil) {
			t.Fatalf("value %d: %+v", tt.value, a)
		}
	}
	if got := CriticalAlertMessage(52, TrendFalling); got != "A glicemia está em 52 mg/dL e a descer." {
		t.Fatalf("message = %q", got)
	}
}

func TestReadingCheck(t *testing.T) {
	now := t0.Add(time.Hour)
	ok := ReadingInput{IdempotencyKey: "k", MeasuredAt: t0, ValueMgDl: 120, Source: SourceCGM}
	if code, msg := ok.Check(now); code != "" {
		t.Fatalf("valid reading rejected: %s %s", code, msg)
	}
	device := uuid.New()
	tests := []struct {
		name   string
		change func(*ReadingInput)
		want   string
	}{
		{"five minutes ahead is fine", func(r *ReadingInput) { r.MeasuredAt = now.Add(4 * time.Minute) }, ""},
		{"more is in the future", func(r *ReadingInput) { r.MeasuredAt = now.Add(6 * time.Minute) }, RejectFuture},
		{"below 20", func(r *ReadingInput) { r.ValueMgDl = 19 }, RejectValidation},
		{"above 550", func(r *ReadingInput) { r.ValueMgDl = 551 }, RejectValidation},
		{"before 2020", func(r *ReadingInput) { r.MeasuredAt = time.Date(2019, 12, 31, 0, 0, 0, 0, time.UTC) }, RejectValidation},
		{"no key", func(r *ReadingInput) { r.IdempotencyKey = " " }, RejectValidation},
		{"unknown source", func(r *ReadingInput) { r.Source = "dexcom" }, RejectValidation},
		{"unknown trend", func(r *ReadingInput) { r.Trend = "up" }, RejectValidation},
		{"a meter reading has no device", func(r *ReadingInput) { r.Source, r.DeviceID = SourceManual, &device }, RejectValidation},
		{"a sensor reading can have one", func(r *ReadingInput) { r.DeviceID = &device }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := ok
			tt.change(&r)
			if code, _ := r.Check(now); code != tt.want {
				t.Fatalf("code = %q, want %q", code, tt.want)
			}
		})
	}
}
