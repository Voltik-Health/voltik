package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/voltik/voltik/backend/internal/database"
	"github.com/voltik/voltik/backend/internal/domain"
)

const reportColumns = `
	r.id, r.patient_id, r.period_type, r.start_date, r.end_date, r.generated_by_role, r.target_min_mg_dl, r.target_max_mg_dl,
	r.coverage_percent, r.time_in_range_percent, r.time_below_range_percent, r.time_above_range_percent,
	r.mean_glucose_mg_dl, r.std_dev_mg_dl, r.coefficient_of_variation_percent, r.gmi_percent,
	r.hypo_episodes, r.hyper_episodes, r.iris_summary, r.work_impact_analysis, r.pdf_file_id, r.created_at`

func scanReport(row pgx.Row) (domain.Report, error) {
	var r domain.Report
	err := row.Scan(&r.ID, &r.PatientID, &r.PeriodType, &r.StartDate, &r.EndDate, &r.GeneratedByRole, &r.TargetMin, &r.TargetMax,
		&r.CoveragePercent, &r.TimeInRange, &r.TimeBelowRange, &r.TimeAboveRange,
		&r.MeanMgDl, &r.StdDevMgDl, &r.CoefficientOfVar, &r.GMIPercent,
		&r.HypoEpisodes, &r.HyperEpisodes, &r.IrisSummary, &r.WorkImpactAnalysis, &r.PDFFileID, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, domain.ErrNotFound
	}
	return r, err
}

// CreateReport saves a report (UC146, UC147). A weekly report that already
// exists for the patient and week is ErrReportExists.
func (s *Store) CreateReport(ctx context.Context, r domain.Report) (domain.Report, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO clinical_reports (patient_id, period_type, start_date, end_date, generated_by_role,
		       target_min_mg_dl, target_max_mg_dl, coverage_percent, time_in_range_percent, time_below_range_percent,
		       time_above_range_percent, mean_glucose_mg_dl, std_dev_mg_dl, coefficient_of_variation_percent,
		       gmi_percent, hypo_episodes, hyper_episodes, iris_summary, work_impact_analysis)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		RETURNING id`,
		r.PatientID, r.PeriodType, r.StartDate, r.EndDate, r.GeneratedByRole, r.TargetMin, r.TargetMax, r.CoveragePercent,
		r.TimeInRange, r.TimeBelowRange, r.TimeAboveRange, r.MeanMgDl, r.StdDevMgDl, r.CoefficientOfVar,
		r.GMIPercent, r.HypoEpisodes, r.HyperEpisodes, r.IrisSummary, r.WorkImpactAnalysis).Scan(&id)
	if database.IsUniqueViolation(err, "uq_clinical_reports_weekly") {
		return domain.Report{}, domain.ErrReportExists
	}
	if err != nil {
		return domain.Report{}, err
	}
	return s.GetReport(ctx, r.PatientID, id)
}

// GetReport reads one of the patient's live reports.
func (s *Store) GetReport(ctx context.Context, patientID, id uuid.UUID) (domain.Report, error) {
	return scanReport(s.pool.QueryRow(ctx, `SELECT `+reportColumns+` FROM clinical_reports r
		 WHERE r.id = $1 AND r.patient_id = $2 AND r.deleted_at IS NULL`, id, patientID))
}

// ListReports returns one page of the patient's live reports, newest first (UC149).
func (s *Store) ListReports(ctx context.Context, patientID uuid.UUID, limit int, cursor string) ([]domain.Report, string, error) {
	var afterAt *time.Time
	var afterID *uuid.UUID
	if cursor != "" {
		t, raw, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, "", domain.Invalid("cursor", "is not a cursor returned by the API")
		}
		afterAt, afterID = &t, &id
	}
	rows, err := s.pool.Query(ctx, `SELECT `+reportColumns+` FROM clinical_reports r
		 WHERE r.patient_id = $1 AND r.deleted_at IS NULL
		   AND ($2::timestamptz IS NULL OR (r.created_at, r.id) < ($2, $3))
		 ORDER BY r.created_at DESC, r.id DESC LIMIT $4`, patientID, afterAt, afterID, limit+1)
	if err != nil {
		return nil, "", err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Report, error) { return scanReport(row) })
	if err != nil || len(items) <= limit {
		return items, "", err
	}
	items = items[:limit]
	last := items[limit-1]
	return items, encodeCursor(last.CreatedAt, last.ID.String()), nil
}

// DeleteReport deletes a report logically and erases its PDF (UC150). Only
// reports the patient or the system generated can be deleted by the patient.
func (s *Store) DeleteReport(ctx context.Context, patientID, id uuid.UUID) error {
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		r, err := scanReport(tx.QueryRow(ctx, `SELECT `+reportColumns+` FROM clinical_reports r
			 WHERE r.id = $1 AND r.patient_id = $2 AND r.deleted_at IS NULL FOR UPDATE`, id, patientID))
		if err != nil {
			return err
		}
		if r.GeneratedByRole == domain.GeneratedByProfessional {
			return domain.ErrForbidden
		}
		if _, err := tx.Exec(ctx, `
			UPDATE clinical_reports SET deleted_at = now(), pdf_file_id = NULL, pdf_file_url = NULL WHERE id = $1`, id); err != nil {
			return err
		}
		if r.PDFFileID != nil {
			_, err = tx.Exec(ctx, `DELETE FROM stored_files WHERE id = $1`, *r.PDFFileID)
		}
		return err
	})
}

// SetReportPDF stores the PDF of a report and links it.
func (s *Store) SetReportPDF(ctx context.Context, patientID, reportID uuid.UUID, pdf domain.StoredFile) (uuid.UUID, error) {
	var fileID uuid.UUID
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var existing *uuid.UUID
		err := tx.QueryRow(ctx, `SELECT pdf_file_id FROM clinical_reports
			 WHERE id = $1 AND patient_id = $2 AND deleted_at IS NULL FOR UPDATE`, reportID, patientID).Scan(&existing)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if existing != nil { // another request generated it first
			fileID = *existing
			return nil
		}
		pdf.PatientID = patientID
		if fileID, err = insertFile(ctx, tx, pdf); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE clinical_reports SET pdf_file_id = $2, pdf_file_url = $3 WHERE id = $1`,
			reportID, fileID, "/v1/files/"+fileID.String())
		return err
	})
	return fileID, err
}

// RecordReportExport writes report_pdf_export to the audit log (UC148).
func (s *Store) RecordReportExport(ctx context.Context, actor, patientID uuid.UUID, meta domain.RequestMeta) error {
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		return insertAudit(ctx, tx, audit{actor: &actor, patient: &patientID, operation: domain.AuditReportPDFExport, meta: meta})
	})
}

// PatientCard reads the name, the birth date and the limits of a live patient.
func (s *Store) PatientCard(ctx context.Context, patientID uuid.UUID) (domain.PatientCard, error) {
	var c domain.PatientCard
	err := s.pool.QueryRow(ctx, `
		SELECT p.id, p.user_id, u.full_name, p.birth_date, p.target_min_mg_dl, p.target_max_mg_dl,
		       p.critical_low_mg_dl, p.critical_high_mg_dl
		  FROM patients p JOIN users u ON u.id = p.user_id
		 WHERE p.id = $1 AND p.deleted_at IS NULL`, patientID).
		Scan(&c.PatientID, &c.UserID, &c.FullName, &c.BirthDate, &c.TargetMin, &c.TargetMax, &c.CriticalLow, &c.CriticalHigh)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, domain.ErrPatientProfileNotFound
	}
	return c, err
}

// WeeklyReportCandidates are the live patients with the weekly report on,
// with readings in [from, to), whose report for the week starting on
// startDate was not generated yet (UC147).
func (s *Store) WeeklyReportCandidates(ctx context.Context, from, to, startDate time.Time) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.id FROM patients p JOIN users u ON u.id = p.user_id
		 WHERE p.deleted_at IS NULL AND p.weekly_reports_enabled
		   AND u.deleted_at IS NULL AND u.account_status = 'active'
		   AND EXISTS (SELECT 1 FROM glucose_readings g
		                WHERE g.patient_id = p.id AND g.deleted_at IS NULL AND g.measured_at >= $1 AND g.measured_at < $2)
		   AND NOT EXISTS (SELECT 1 FROM clinical_reports r
		                    WHERE r.patient_id = p.id AND r.generated_by_role = 'system_scheduled' AND r.start_date = $3)`,
		from, to, startDate)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}
