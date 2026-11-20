// Package reportpdf draws the PDF of a clinical report (SRS UC148): the
// patient, the metrics, the AGP chart (ambulatory glucose profile: the
// percentiles of glucose at each time of day over the period) and the Iris
// summary. It uses the PDF core fonts, so no font file is embedded; text is
// converted to Windows-1252, which covers Portuguese.
package reportpdf

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"

	"github.com/voltik/voltik/backend/internal/domain"
)

// Input is everything the PDF shows.
type Input struct {
	PatientName string
	BirthDate   time.Time
	Report      domain.Report
	Points      []domain.Point // the readings of the period, for the AGP chart
	GeneratedAt time.Time
}

const (
	pageMargin = 15.0
	binMinutes = 15
	chartMin   = 40.0
	chartMax   = 400.0
)

// Render returns the PDF bytes.
func Render(in Input) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(pageMargin, pageMargin, pageMargin)
	pdf.SetAutoPageBreak(true, 20)
	tr := pdf.UnicodeTranslatorFromDescriptor("") // UTF-8 to cp1252 for the core fonts
	pdf.SetFooterFunc(func() {
		pdf.SetY(-15)
		pdf.SetFont("Helvetica", "I", 7.5)
		pdf.SetTextColor(110, 110, 110)
		pdf.MultiCell(0, 3.5, tr("O Voltik não é um dispositivo médico. Este relatório é informativo e não substitui a avaliação da sua equipa de saúde. "+
			fmt.Sprintf("Página %d.", pdf.PageNo())), "", "C", false)
	})
	pdf.AddPage()
	r := in.Report
	width, _ := pdf.GetPageSize()
	content := width - 2*pageMargin

	// Title and patient
	pdf.SetFont("Helvetica", "B", 16)
	pdf.SetTextColor(20, 40, 80)
	pdf.CellFormat(content, 8, tr("Voltik · Relatório de glicemia"), "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(40, 40, 40)
	pdf.CellFormat(content, 5.5, tr(fmt.Sprintf("Período: %s a %s (%s)", date(r.StartDate), date(r.EndDate), periodName(r.PeriodType))), "", 1, "L", false, 0, "")
	pdf.CellFormat(content, 5.5, tr(fmt.Sprintf("Paciente: %s · Data de nascimento: %s", in.PatientName, date(in.BirthDate))), "", 1, "L", false, 0, "")
	pdf.CellFormat(content, 5.5, tr(fmt.Sprintf("Gerado em %s (hora de Portugal) · %s", in.GeneratedAt.In(domain.Lisbon).Format("02/01/2006 15:04"), generatedBy(r.GeneratedByRole))), "", 1, "L", false, 0, "")
	pdf.Ln(2)

	if r.LowCoverage() {
		pdf.SetFillColor(255, 243, 205)
		pdf.SetTextColor(120, 80, 0)
		pdf.SetFont("Helvetica", "B", 9.5)
		pdf.MultiCell(content, 5.5, tr(fmt.Sprintf("Dados insuficientes para conclusões clínicas: só %.0f %% do período tem leituras (recomenda-se pelo menos 70 %%).",
			*r.CoveragePercent)), "", "L", true)
		pdf.Ln(2)
	}

	// Metrics
	targetMin, targetMax := 70, 180
	if r.TargetMin != nil && r.TargetMax != nil {
		targetMin, targetMax = *r.TargetMin, *r.TargetMax
	}
	section(pdf, tr, "Métricas do período")
	rows := [][2]string{
		{fmt.Sprintf("Tempo no alvo (TIR, %d–%d mg/dL)", targetMin, targetMax), pct(r.TimeInRange)},
		{fmt.Sprintf("Tempo abaixo do alvo (TBR, < %d mg/dL)", targetMin), pct(r.TimeBelowRange)},
		{fmt.Sprintf("Tempo acima do alvo (TAR, > %d mg/dL)", targetMax), pct(r.TimeAboveRange)},
		{"Glicemia média", fmt.Sprintf("%d mg/dL", r.MeanMgDl)},
		{"Desvio padrão", optional(r.StdDevMgDl, "%.1f mg/dL")},
		{"Coeficiente de variação (alvo: até 36 %)", optional(r.CoefficientOfVar, "%.1f %%")},
		{"GMI (HbA1c estimada)", optional(r.GMIPercent, "%.2f %%")},
		{"Episódios de hipoglicemia (15 min ou mais)", fmt.Sprintf("%d", r.HypoEpisodes)},
		{"Episódios de hiperglicemia (15 min ou mais)", fmt.Sprintf("%d", r.HyperEpisodes)},
	}
	if r.CoveragePercent != nil {
		rows = append(rows, [2]string{"Cobertura de dados", pct(*r.CoveragePercent)})
	}
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(30, 30, 30)
	for i, row := range rows {
		fill := i%2 == 0
		pdf.SetFillColor(242, 245, 250)
		pdf.CellFormat(content*0.7, 6.5, tr(row[0]), "", 0, "L", fill, 0, "")
		pdf.CellFormat(content*0.3, 6.5, tr(row[1]), "", 1, "R", fill, 0, "")
	}
	pdf.Ln(3)

	// AGP chart
	section(pdf, tr, "Perfil glicémico ambulatório (AGP)")
	pdf.SetFont("Helvetica", "", 8.5)
	pdf.SetTextColor(80, 80, 80)
	pdf.MultiCell(content, 4.2, tr("Glicemia a cada hora do dia, juntando todos os dias do período: a linha é a mediana, a faixa escura vai do percentil 25 ao 75 e a clara do 5 ao 95. As linhas verdes marcam o intervalo alvo."), "", "L", false)
	pdf.Ln(1)
	drawAGP(pdf, tr, AGP(in.Points), pageMargin, pdf.GetY(), content, 70, float64(targetMin), float64(targetMax))
	pdf.SetY(pdf.GetY() + 80)

	// Iris
	if r.IrisSummary != nil || r.WorkImpactAnalysis != nil {
		section(pdf, tr, "Resumo da Iris (escrito por IA, informativo)")
		pdf.SetFont("Helvetica", "", 10)
		pdf.SetTextColor(30, 30, 30)
		if r.IrisSummary != nil {
			pdf.MultiCell(content, 5, tr(*r.IrisSummary), "", "L", false)
			pdf.Ln(1.5)
		}
		if r.WorkImpactAnalysis != nil {
			pdf.SetFont("Helvetica", "B", 10)
			pdf.CellFormat(content, 5.5, tr("Impacto no dia de trabalho"), "", 1, "L", false, 0, "")
			pdf.SetFont("Helvetica", "", 10)
			pdf.MultiCell(content, 5, tr(*r.WorkImpactAnalysis), "", "L", false)
		}
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func section(pdf *fpdf.Fpdf, tr func(string) string, title string) {
	pdf.SetFont("Helvetica", "B", 11.5)
	pdf.SetTextColor(20, 40, 80)
	w, _ := pdf.GetPageSize()
	pdf.CellFormat(w-2*pageMargin, 7, tr(title), "B", 1, "L", false, 0, "")
	pdf.Ln(1.5)
}

// Bin is one time of day of the AGP: the percentiles of the readings taken
// in those 15 minutes, over every day of the period.
type Bin struct {
	Minute                 int // minutes after midnight, in Portugal
	P5, P25, P50, P75, P95 float64
	Count                  int
}

// AGP groups the readings by time of day (15-minute bins). Bins with fewer
// than 3 readings are left out.
func AGP(points []domain.Point) []Bin {
	groups := make([][]float64, 24*60/binMinutes)
	for _, p := range points {
		local := p.At.In(domain.Lisbon)
		i := (local.Hour()*60 + local.Minute()) / binMinutes
		groups[i] = append(groups[i], float64(p.Value))
	}
	var bins []Bin
	for i, values := range groups {
		if len(values) < 3 {
			continue
		}
		sort.Float64s(values)
		bins = append(bins, Bin{
			Minute: i * binMinutes, Count: len(values),
			P5: percentile(values, 5), P25: percentile(values, 25), P50: percentile(values, 50),
			P75: percentile(values, 75), P95: percentile(values, 95),
		})
	}
	return bins
}

// percentile of sorted values, with linear interpolation.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := p / 100 * float64(len(sorted)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	return sorted[lo] + (sorted[hi]-sorted[lo])*(rank-float64(lo))
}

func drawAGP(pdf *fpdf.Fpdf, tr func(string) string, bins []Bin, x, y, w, h, targetMin, targetMax float64) {
	left := x + 10
	plotW := w - 12
	px := func(minute float64) float64 { return left + plotW*minute/(24*60) }
	py := func(v float64) float64 {
		v = math.Max(chartMin, math.Min(chartMax, v))
		return y + h - h*(v-chartMin)/(chartMax-chartMin)
	}
	pdf.SetDrawColor(200, 200, 200)
	pdf.SetLineWidth(0.2)
	pdf.Rect(left, y, plotW, h, "D")
	pdf.SetFont("Helvetica", "", 7)
	pdf.SetTextColor(100, 100, 100)
	for _, v := range []float64{54, targetMin, targetMax, 250, 350} {
		pdf.Line(left, py(v), left+plotW, py(v))
		pdf.Text(x, py(v)+1, fmt.Sprintf("%.0f", v))
	}
	for hour := 0; hour <= 24; hour += 3 {
		pdf.Text(px(float64(hour*60))-2, y+h+4, fmt.Sprintf("%02dh", hour))
	}
	if len(bins) == 0 {
		pdf.SetFont("Helvetica", "I", 9)
		pdf.Text(left+plotW/2-30, y+h/2, tr("Sem leituras suficientes para o gráfico."))
		return
	}
	// contiguous runs of bins
	var runs [][]Bin
	for i, b := range bins {
		if i == 0 || b.Minute != bins[i-1].Minute+binMinutes {
			runs = append(runs, nil)
		}
		runs[len(runs)-1] = append(runs[len(runs)-1], b)
	}
	band := func(run []Bin, lo, hi func(Bin) float64) []fpdf.PointType {
		var pts []fpdf.PointType
		for _, b := range run {
			pts = append(pts, fpdf.PointType{X: px(float64(b.Minute) + binMinutes/2.0), Y: py(hi(b))})
		}
		for i := len(run) - 1; i >= 0; i-- {
			b := run[i]
			pts = append(pts, fpdf.PointType{X: px(float64(b.Minute) + binMinutes/2.0), Y: py(lo(b))})
		}
		return pts
	}
	for _, run := range runs {
		if len(run) < 2 {
			continue
		}
		pdf.SetFillColor(205, 222, 245)
		pdf.Polygon(band(run, func(b Bin) float64 { return b.P5 }, func(b Bin) float64 { return b.P95 }), "F")
		pdf.SetFillColor(120, 160, 220)
		pdf.Polygon(band(run, func(b Bin) float64 { return b.P25 }, func(b Bin) float64 { return b.P75 }), "F")
		pdf.SetDrawColor(20, 50, 120)
		pdf.SetLineWidth(0.6)
		for i := 1; i < len(run); i++ {
			a, b := run[i-1], run[i]
			pdf.Line(px(float64(a.Minute)+binMinutes/2.0), py(a.P50), px(float64(b.Minute)+binMinutes/2.0), py(b.P50))
		}
	}
	pdf.SetDrawColor(40, 160, 80)
	pdf.SetLineWidth(0.5)
	pdf.Line(left, py(targetMin), left+plotW, py(targetMin))
	pdf.Line(left, py(targetMax), left+plotW, py(targetMax))
}

func date(t time.Time) string { return t.Format("02/01/2006") }

func pct(v float64) string { return strings.Replace(fmt.Sprintf("%.1f %%", v), ".", ",", 1) }

func optional(v *float64, format string) string {
	if v == nil {
		return "–"
	}
	return strings.Replace(fmt.Sprintf(format, *v), ".", ",", 1)
}

func periodName(p string) string {
	switch p {
	case domain.PeriodDay:
		return "dia"
	case domain.PeriodWeek:
		return "semana"
	case domain.PeriodMonth:
		return "mês"
	}
	return "personalizado"
}

func generatedBy(role string) string {
	switch role {
	case domain.GeneratedByProfessional:
		return "gerado pelo profissional de saúde"
	case domain.GeneratedBySystem:
		return "relatório semanal automático"
	}
	return "gerado pelo paciente"
}
