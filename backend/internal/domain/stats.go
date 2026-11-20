package domain

import (
	"errors"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
)

// Point is one glucose value in time, for statistics and charts.
type Point struct {
	At    time.Time
	Value int
}

// Stats are the metrics of the international consensus on time in range
// (Battelino et al., 2019 and 2023), computed with the patient's target range.
type Stats struct {
	From, To         time.Time
	TargetMin        int
	TargetMax        int
	ReadingsCount    int
	CoveragePercent  float64
	TimeInRange      *float64
	TimeBelowRange   *float64
	TimeAboveRange   *float64
	MeanMgDl         *int
	StdDevMgDl       *float64
	CoefficientOfVar *float64
	GMIPercent       *float64
	HypoEpisodes     int
	HyperEpisodes    int
	ActiveMinutes    int
	LowCoverage      bool
}

const (
	// Each reading stands for the time until the next one, up to this.
	sampleWindow = 5 * time.Minute
	// An episode lasts at least this long below or above the range.
	episodeMinimum = 15 * time.Minute
	// Below this share of 5-minute slots with data, conclusions are not reliable.
	minCoveragePercent = 70
)

// ComputeStats works out the metrics of the readings in [from, to). The
// readings may come in any order and at any interval (5 minutes from a CGM,
// 1 minute from Juggluco, a few a day from a meter): each one weighs the time
// until the next reading, up to 5 minutes.
func ComputeStats(points []Point, from, to time.Time, t Thresholds, activeMinutes int) Stats {
	s := Stats{From: from, To: to, TargetMin: t.TargetMin, TargetMax: t.TargetMax, ActiveMinutes: activeMinutes}
	in := make([]Point, 0, len(points))
	for _, p := range points {
		if !p.At.Before(from) && p.At.Before(to) {
			in = append(in, p)
		}
	}
	sort.Slice(in, func(i, j int) bool { return in[i].At.Before(in[j].At) })
	s.ReadingsCount = len(in)
	s.CoveragePercent = coverage(in, from, to)
	s.LowCoverage = s.CoveragePercent < minCoveragePercent
	if len(in) == 0 {
		return s
	}

	weights := make([]float64, len(in))
	var total, below, above, sum float64
	for i, p := range in {
		w := sampleWindow
		if i+1 < len(in) {
			if gap := in[i+1].At.Sub(p.At); gap < w {
				w = gap
			}
		}
		if end := to.Sub(p.At); end < w {
			w = end
		}
		weights[i] = w.Minutes()
		total += weights[i]
		sum += weights[i] * float64(p.Value)
		switch {
		case p.Value < t.TargetMin:
			below += weights[i]
		case p.Value > t.TargetMax:
			above += weights[i]
		}
	}
	if total == 0 { // only readings at the same instant
		for i := range weights {
			weights[i] = 1
		}
		total, below, above, sum = float64(len(in)), 0, 0, 0
		for _, p := range in {
			sum += float64(p.Value)
			switch {
			case p.Value < t.TargetMin:
				below++
			case p.Value > t.TargetMax:
				above++
			}
		}
	}

	// TIR is what is left, so the three always add up to 100 % (ck_report_tir_sum)
	tbr := round1(100 * below / total)
	tar := round1(100 * above / total)
	tir := round1(100 - tbr - tar)
	mean := sum / total
	var sq float64
	for i, p := range in {
		d := float64(p.Value) - mean
		sq += weights[i] * d * d
	}
	sd := round1(math.Sqrt(sq / total))
	cv := round1(100 * sd / mean)
	gmi := round2(3.31 + 0.02392*mean)
	m := int(math.Round(mean))

	s.TimeInRange, s.TimeBelowRange, s.TimeAboveRange = &tir, &tbr, &tar
	s.MeanMgDl, s.StdDevMgDl, s.CoefficientOfVar, s.GMIPercent = &m, &sd, &cv, &gmi
	s.HypoEpisodes = episodes(in, func(v int) bool { return v < t.TargetMin })
	s.HyperEpisodes = episodes(in, func(v int) bool { return v > t.TargetMax })
	return s
}

// coverage is the share of 5-minute slots of [from, to) that have a reading.
func coverage(points []Point, from, to time.Time) float64 {
	slots := int(math.Ceil(to.Sub(from).Minutes() / sampleWindow.Minutes()))
	if slots <= 0 {
		return 0
	}
	seen := map[int]bool{}
	for _, p := range points {
		seen[int(p.At.Sub(from)/sampleWindow)] = true
	}
	return round1(100 * float64(len(seen)) / float64(slots))
}

// episodes counts the runs of readings that satisfy out for at least 15
// minutes. A gap of more than 15 minutes without data ends a run.
func episodes(points []Point, out func(int) bool) int {
	count := 0
	var runStart, runEnd time.Time
	inRun := false
	closeRun := func() {
		if inRun && runEnd.Sub(runStart) >= episodeMinimum {
			count++
		}
		inRun = false
	}
	for i, p := range points {
		if !out(p.Value) {
			closeRun()
			continue
		}
		end := p.At.Add(sampleWindow)
		if i+1 < len(points) && points[i+1].At.Sub(p.At) < sampleWindow {
			end = points[i+1].At
		}
		switch {
		case !inRun:
			runStart, runEnd, inRun = p.At, end, true
		case p.At.Sub(runEnd) > episodeMinimum:
			closeRun()
			runStart, runEnd, inRun = p.At, end, true
		default:
			runEnd = end
		}
	}
	closeRun()
	return count
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }

// Report periods and who generated a report.
const (
	PeriodDay    = "day"
	PeriodWeek   = "week"
	PeriodMonth  = "month"
	PeriodCustom = "custom"

	GeneratedByPatient      = "patient"
	GeneratedByProfessional = "professional"
	GeneratedBySystem       = "system_scheduled"
)

// Report is a row of clinical_reports.
type Report struct {
	ID                 uuid.UUID
	PatientID          uuid.UUID
	PeriodType         string
	StartDate          time.Time
	EndDate            time.Time
	GeneratedByRole    string
	TargetMin          *int
	TargetMax          *int
	CoveragePercent    *float64
	TimeInRange        float64
	TimeBelowRange     float64
	TimeAboveRange     float64
	MeanMgDl           int
	StdDevMgDl         *float64
	CoefficientOfVar   *float64
	GMIPercent         *float64
	HypoEpisodes       int
	HyperEpisodes      int
	IrisSummary        *string
	WorkImpactAnalysis *string
	PDFFileID          *uuid.UUID
	CreatedAt          time.Time
}

// LowCoverage reports whether the report had too little data for conclusions.
func (r Report) LowCoverage() bool {
	return r.CoveragePercent != nil && *r.CoveragePercent < minCoveragePercent
}

// ReportFromStats builds the report row of a period.
func ReportFromStats(s Stats, patientID uuid.UUID, period string, start, end time.Time, by string) Report {
	cov := s.CoveragePercent
	min, max := s.TargetMin, s.TargetMax
	return Report{
		PatientID: patientID, PeriodType: period, StartDate: start, EndDate: end, GeneratedByRole: by,
		TargetMin: &min, TargetMax: &max, CoveragePercent: &cov,
		TimeInRange: *s.TimeInRange, TimeBelowRange: *s.TimeBelowRange, TimeAboveRange: *s.TimeAboveRange,
		MeanMgDl: *s.MeanMgDl, StdDevMgDl: s.StdDevMgDl, CoefficientOfVar: s.CoefficientOfVar, GMIPercent: s.GMIPercent,
		HypoEpisodes: s.HypoEpisodes, HyperEpisodes: s.HyperEpisodes,
	}
}

// ValidateReportPeriod checks the days of a report: at most 90, and not after today.
func ValidateReportPeriod(period string, start, end, today time.Time) error {
	var v ValidationError
	switch period {
	case PeriodDay, PeriodWeek, PeriodMonth, PeriodCustom:
	default:
		v.Add("period_type", "must be one of the listed values")
	}
	switch {
	case end.Before(start):
		v.Add("end_date", "must not be before start_date")
	case end.Sub(start) >= MaxRangeDays*24*time.Hour:
		v.Add("end_date", "the period can be at most 90 days")
	case end.After(today):
		v.Add("end_date", "must not be after today")
	}
	return v.OrNil()
}

// HourProfile is the mean glucose of each block of the day, used by Iris to
// describe the impact on the working day without knowing who the patient is.
type HourProfile struct {
	Block         string
	WeekdayMean   *int
	WeekendMean   *int
	WeekdayTIR    *float64
	ReadingsCount int
}

// DayBlocks splits the day (in Portugal) for the work impact analysis.
var DayBlocks = []struct {
	Name       string
	Start, End int // hours
}{
	{"madrugada (00h–06h)", 0, 6},
	{"manhã (06h–12h)", 6, 12},
	{"início da tarde (12h–15h)", 12, 15},
	{"tarde (15h–19h)", 15, 19},
	{"noite (19h–24h)", 19, 24},
}

// Profile groups the readings by block of the day and by weekday or weekend.
func Profile(points []Point, t Thresholds) []HourProfile {
	type acc struct {
		sum, n, inRange int
	}
	weekday := make([]acc, len(DayBlocks))
	weekend := make([]acc, len(DayBlocks))
	for _, p := range points {
		local := p.At.In(Lisbon)
		for i, b := range DayBlocks {
			if local.Hour() < b.Start || local.Hour() >= b.End {
				continue
			}
			target := &weekday[i]
			if wd := local.Weekday(); wd == time.Saturday || wd == time.Sunday {
				target = &weekend[i]
			}
			target.sum += p.Value
			target.n++
			if p.Value >= t.TargetMin && p.Value <= t.TargetMax {
				target.inRange++
			}
		}
	}
	out := make([]HourProfile, len(DayBlocks))
	for i, b := range DayBlocks {
		out[i] = HourProfile{Block: b.Name, ReadingsCount: weekday[i].n + weekend[i].n}
		if a := weekday[i]; a.n > 0 {
			m := int(math.Round(float64(a.sum) / float64(a.n)))
			tir := round1(100 * float64(a.inRange) / float64(a.n))
			out[i].WeekdayMean, out[i].WeekdayTIR = &m, &tir
		}
		if a := weekend[i]; a.n > 0 {
			m := int(math.Round(float64(a.sum) / float64(a.n)))
			out[i].WeekendMean = &m
		}
	}
	return out
}

// Postprandial is the largest change of glucose in the 2 hours after a meal,
// compared with the reading closest to the start (at most 15 minutes before).
// It returns false when there are not enough readings (UC108 A1).
func Postprandial(points []Point, eatenAt time.Time) (int, bool) {
	var base *Point
	var after []Point
	for i, p := range points {
		switch {
		case !p.At.After(eatenAt) && eatenAt.Sub(p.At) <= 15*time.Minute:
			if base == nil || p.At.After(base.At) {
				base = &points[i]
			}
		case p.At.After(eatenAt) && !p.At.After(eatenAt.Add(2*time.Hour)):
			after = append(after, p)
		}
	}
	if base == nil || len(after) < 6 {
		return 0, false
	}
	best := 0
	for _, p := range after {
		if d := p.Value - base.Value; abs(d) > abs(best) {
			best = d
		}
	}
	return best, true
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// ErrReportExists means the weekly report of that week was already generated.
var ErrReportExists = errors.New("the weekly report already exists")

// PatientCard is what reports, PDFs and alerts need about a patient.
type PatientCard struct {
	PatientID uuid.UUID
	UserID    uuid.UUID
	FullName  string
	BirthDate time.Time
	Thresholds
}
