package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
	"github.com/voltik/voltik/backend/internal/reportpdf"
)

// HistoryStore is what statistics and reports need from the database.
type HistoryStore interface {
	PatientCard(ctx context.Context, patientID uuid.UUID) (domain.PatientCard, error)
	ReadingPoints(ctx context.Context, patientID uuid.UUID, from, to time.Time) ([]domain.Point, error)
	ActiveMinutes(ctx context.Context, patientID uuid.UUID, from, to time.Time) (int, error)
	HasActiveConsent(ctx context.Context, userID uuid.UUID, docType string) (bool, error)
	CreateReport(ctx context.Context, r domain.Report) (domain.Report, error)
	GetReport(ctx context.Context, patientID, id uuid.UUID) (domain.Report, error)
	ListReports(ctx context.Context, patientID uuid.UUID, limit int, cursor string) ([]domain.Report, string, error)
	DeleteReport(ctx context.Context, patientID, id uuid.UUID) error
	SetReportPDF(ctx context.Context, patientID, reportID uuid.UUID, pdf domain.StoredFile) (uuid.UUID, error)
	WeeklyReportCandidates(ctx context.Context, from, to, startDate time.Time) ([]uuid.UUID, error)
}

// History runs the statistics of a period and the clinical reports
// (UC086, UC146–UC150).
type History struct {
	store    HistoryStore
	ai       AI
	notifier Notifier
	files    *Files
	log      *slog.Logger
	now      func() time.Time
}

func NewHistory(store HistoryStore, ai AI, notifier Notifier, files *Files, log *slog.Logger) *History {
	return &History{store: store, ai: ai, notifier: notifier, files: files, log: log, now: time.Now}
}

// Stats computes the metrics of [from, to) with the patient's limits.
func (h *History) Stats(ctx context.Context, a domain.Account, from, to time.Time) (domain.Stats, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.Stats{}, err
	}
	if err := domain.CheckRange(from, to); err != nil {
		return domain.Stats{}, err
	}
	return h.stats(ctx, pid, from, to)
}

func (h *History) stats(ctx context.Context, patientID uuid.UUID, from, to time.Time) (domain.Stats, error) {
	card, err := h.store.PatientCard(ctx, patientID)
	if err != nil {
		return domain.Stats{}, err
	}
	points, err := h.store.ReadingPoints(ctx, patientID, from, to)
	if err != nil {
		return domain.Stats{}, err
	}
	active, err := h.store.ActiveMinutes(ctx, patientID, from, to)
	if err != nil {
		return domain.Stats{}, err
	}
	return domain.ComputeStats(points, from, to, card.Thresholds, active), nil
}

// CreateReport generates and saves the report of whole days (UC146).
func (h *History) CreateReport(ctx context.Context, a domain.Account, period string, start, end time.Time) (domain.Report, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.Report{}, err
	}
	if err := domain.ValidateReportPeriod(period, start, end, domain.LisbonDate(h.now())); err != nil {
		return domain.Report{}, err
	}
	return h.generate(ctx, pid, a.ID, period, start, end, domain.GeneratedByPatient)
}

func (h *History) generate(ctx context.Context, patientID, userID uuid.UUID, period string, start, end time.Time, by string) (domain.Report, error) {
	from, to := domain.DayStart(start), domain.DayStart(end.AddDate(0, 0, 1))
	s, err := h.stats(ctx, patientID, from, to)
	if err != nil {
		return domain.Report{}, err
	}
	if s.ReadingsCount == 0 {
		return domain.Report{}, domain.ErrNoReadings
	}
	r := domain.ReportFromStats(s, patientID, period, start, end, by)
	consent, err := h.store.HasActiveConsent(ctx, userID, domain.DocExternalAIProcessing)
	if err != nil {
		return domain.Report{}, err
	}
	if consent && h.ai != nil && h.ai.Configured() {
		points, err := h.store.ReadingPoints(ctx, patientID, from, to)
		if err == nil {
			card, _ := h.store.PatientCard(ctx, patientID)
			r.IrisSummary, r.WorkImpactAnalysis = h.summary(ctx, s, domain.Profile(points, card.Thresholds), end.Sub(start)/(24*time.Hour)+1)
		}
	}
	return h.store.CreateReport(ctx, r)
}

// summaryPrompt gives Iris only numbers and times of day: never who the
// patient is (RNF06). The rules keep it away from doses and diagnoses (RNF07).
const summaryPrompt = `És a Iris, a assistente do Voltik, uma app de acompanhamento da diabetes.
Escreve em português de Portugal, com frases simples e tom positivo, para uma pessoa sem formação em saúde.
Regras obrigatórias: nunca sugiras doses de insulina nem alterações de medicação, nunca faças diagnósticos,
e recomenda falar com a equipa de saúde quando houver valores preocupantes.
Devolve:
- "summary": um resumo do período em 2 a 4 frases (no máximo 80 palavras).
- "work_impact": em 1 a 3 frases (no máximo 60 palavras), como a glicemia variou ao longo do dia de trabalho
  (dias úteis comparados com o fim de semana), usando os blocos do dia.
Dados do período (sem identificação da pessoa):
`

var summarySchema = map[string]any{
	"type": "OBJECT",
	"properties": map[string]any{
		"summary":     map[string]any{"type": "STRING"},
		"work_impact": map[string]any{"type": "STRING"},
	},
	"required": []string{"summary", "work_impact"},
}

// forbidden catches answers that talk about doses or medication (RNF07).
var forbidden = regexp.MustCompile(`(?i)\b(dose|doses|unidades|insulina rápida|aument\w* a insulina|diminu\w* a insulina|medicaç|diagnóstic)`)

func (h *History) summary(ctx context.Context, s domain.Stats, profile []domain.HourProfile, days time.Duration) (*string, *string) {
	var b strings.Builder
	b.WriteString(summaryPrompt)
	fmt.Fprintf(&b, "- duração: %d dias; cobertura de dados: %.0f %%\n", int(days), s.CoveragePercent)
	fmt.Fprintf(&b, "- intervalo alvo: %d a %d mg/dL\n", s.TargetMin, s.TargetMax)
	fmt.Fprintf(&b, "- tempo no alvo: %.1f %%; abaixo: %.1f %%; acima: %.1f %%\n", *s.TimeInRange, *s.TimeBelowRange, *s.TimeAboveRange)
	fmt.Fprintf(&b, "- média: %d mg/dL; coeficiente de variação: %.1f %%; GMI: %.2f %%\n", *s.MeanMgDl, *s.CoefficientOfVar, *s.GMIPercent)
	fmt.Fprintf(&b, "- episódios de hipoglicemia: %d; de hiperglicemia: %d; minutos de atividade física: %d\n", s.HypoEpisodes, s.HyperEpisodes, s.ActiveMinutes)
	for _, p := range profile {
		if p.ReadingsCount == 0 {
			continue
		}
		fmt.Fprintf(&b, "- %s: média nos dias úteis %s, no fim de semana %s; tempo no alvo nos dias úteis %s\n",
			p.Block, mgdl(p.WeekdayMean), mgdl(p.WeekendMean), percent(p.WeekdayTIR))
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	var out struct {
		Summary    string `json:"summary"`
		WorkImpact string `json:"work_impact"`
	}
	if _, err := h.ai.GenerateJSON(ctx, b.String(), nil, summarySchema, &out); err != nil {
		h.log.Warn("report summary: Iris did not answer", "error", err)
		return nil, nil
	}
	clean := func(text string) *string {
		text = strings.TrimSpace(text)
		if text == "" || forbidden.MatchString(text) || len([]rune(text)) > 1200 {
			return nil
		}
		return &text
	}
	return clean(out.Summary), clean(out.WorkImpact)
}

func mgdl(v *int) string {
	if v == nil {
		return "sem dados"
	}
	return strconv.Itoa(*v) + " mg/dL"
}

func percent(v *float64) string {
	if v == nil {
		return "sem dados"
	}
	return fmt.Sprintf("%.0f %%", *v)
}

func (h *History) ListReports(ctx context.Context, a domain.Account, limit int, cursor string) ([]domain.Report, string, error) {
	pid, err := patientOf(a)
	if err != nil {
		return nil, "", err
	}
	return h.store.ListReports(ctx, pid, limit, cursor)
}

func (h *History) GetReport(ctx context.Context, a domain.Account, id uuid.UUID) (domain.Report, error) {
	pid, err := patientOf(a)
	if err != nil {
		return domain.Report{}, err
	}
	return h.store.GetReport(ctx, pid, id)
}

func (h *History) DeleteReport(ctx context.Context, a domain.Account, id uuid.UUID) error {
	pid, err := patientOf(a)
	if err != nil {
		return err
	}
	return h.store.DeleteReport(ctx, pid, id)
}

// ExportPDF returns a 15-minute link to the report's PDF, generating it the
// first time (UC148). Clinicians and caregivers export through DVT-87, which
// also records report_pdf_export in the audit log.
func (h *History) ExportPDF(ctx context.Context, a domain.Account, id uuid.UUID) (string, time.Time, error) {
	pid, err := patientOf(a)
	if err != nil {
		return "", time.Time{}, err
	}
	r, err := h.store.GetReport(ctx, pid, id)
	if err != nil {
		return "", time.Time{}, err
	}
	fileID := r.PDFFileID
	if fileID == nil {
		card, err := h.store.PatientCard(ctx, pid)
		if err != nil {
			return "", time.Time{}, err
		}
		points, err := h.store.ReadingPoints(ctx, pid, domain.DayStart(r.StartDate), domain.DayStart(r.EndDate.AddDate(0, 0, 1)))
		if err != nil {
			return "", time.Time{}, err
		}
		pdf, err := reportpdf.Render(reportpdf.Input{
			PatientName: card.FullName, BirthDate: card.BirthDate, Report: r, Points: points, GeneratedAt: h.now(),
		})
		if err != nil {
			return "", time.Time{}, err
		}
		id, err := h.store.SetReportPDF(ctx, pid, r.ID, domain.StoredFile{Purpose: domain.FileReportPDF, ContentType: "application/pdf", Data: pdf})
		if err != nil {
			return "", time.Time{}, err
		}
		fileID = &id
	}
	url, expires := h.files.Link(*fileID)
	return url, expires, nil
}

// WeeklyReports is the job of UC147: on Monday from 06:00 (in Portugal), the
// report of the week before for every patient who has readings and did not
// turn it off. It runs every few minutes; the unique index makes each report
// happen once.
func (h *History) WeeklyReports(ctx context.Context) {
	now := h.now().In(domain.Lisbon)
	if now.Weekday() != time.Monday || now.Hour() < 6 {
		return
	}
	today := domain.LisbonDate(now)
	start, end := today.AddDate(0, 0, -7), today.AddDate(0, 0, -1)
	patients, err := h.store.WeeklyReportCandidates(ctx, domain.DayStart(start), domain.DayStart(today), start)
	if err != nil {
		h.log.Warn("weekly reports: could not read the patients", "error", err)
		return
	}
	made := 0
	for _, pid := range patients {
		card, err := h.store.PatientCard(ctx, pid)
		if err != nil {
			continue
		}
		r, err := h.generate(ctx, pid, card.UserID, domain.PeriodWeek, start, end, domain.GeneratedBySystem)
		switch {
		case errors.Is(err, domain.ErrReportExists), errors.Is(err, domain.ErrNoReadings):
			continue
		case err != nil:
			h.log.Warn("weekly reports: could not generate a report", "error", err)
			continue
		}
		made++
		h.notifier.Notify(ctx, []uuid.UUID{card.UserID}, map[string]string{"type": "report_ready", "report_id": r.ID.String()})
	}
	if made > 0 {
		h.log.Info("weekly reports generated", "count", made)
	}
}

// --- Signed file links ---------------------------------------------------------

// FileStore reads the stored files.
type FileStore interface {
	GetFile(ctx context.Context, id uuid.UUID) (domain.StoredFile, error)
}

// Files signs the links to meal photos and report PDFs and checks them.
// A link works for 15 minutes without the session token, so an image loader
// or a PDF viewer can open it (UC148).
type Files struct {
	key     []byte
	baseURL string
	store   FileStore
	now     func() time.Time
}

// NewFiles derives the signing key from the server secret, so the links
// need no secret of their own.
func NewFiles(secret []byte, baseURL string, store FileStore) *Files {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("voltik file links v1"))
	return &Files{key: mac.Sum(nil), baseURL: strings.TrimRight(baseURL, "/"), store: store, now: time.Now}
}

func (f *Files) sign(id uuid.UUID, expires int64) string {
	mac := hmac.New(sha256.New, f.key)
	fmt.Fprintf(mac, "%s|%d", id, expires)
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

// Link returns the signed link to a file and when it expires.
func (f *Files) Link(id uuid.UUID) (string, time.Time) {
	expires := f.now().Add(domain.FileLinkLifetime).Truncate(time.Second)
	return fmt.Sprintf("%s/v1/files/%s?expires=%d&signature=%s", f.baseURL, id, expires.Unix(), f.sign(id, expires.Unix())), expires.UTC()
}

// Open checks a signed link and returns the file.
func (f *Files) Open(ctx context.Context, id uuid.UUID, expires int64, signature string) (domain.StoredFile, error) {
	if f.now().Unix() > expires || !hmac.Equal([]byte(f.sign(id, expires)), []byte(signature)) {
		return domain.StoredFile{}, domain.ErrLinkExpired
	}
	return f.store.GetFile(ctx, id)
}
