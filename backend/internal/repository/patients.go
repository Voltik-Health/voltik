package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/voltik/voltik/backend/internal/database"
	"github.com/voltik/voltik/backend/internal/domain"
)

const patientColumns = `
	p.id, p.user_id, p.birth_date, g.code, p.occupation, d.code, t.code,
	p.target_min_mg_dl, p.target_max_mg_dl, p.critical_low_mg_dl, p.critical_high_mg_dl,
	p.weight_kg, p.height_cm, p.created_at, p.updated_at`

const patientFrom = `
	  FROM patients p
	  JOIN diabetes_types d ON d.id = p.diabetes_type_id
	  LEFT JOIN genders g ON g.id = p.gender_id
	  LEFT JOIN therapy_types t ON t.id = p.therapy_type_id`

func scanPatient(row pgx.Row) (domain.Patient, error) {
	var p domain.Patient
	err := row.Scan(&p.ID, &p.UserID, &p.BirthDate, &p.Gender, &p.Occupation, &p.DiabetesType, &p.Therapy,
		&p.TargetMin, &p.TargetMax, &p.CriticalLow, &p.CriticalHigh,
		&p.WeightKg, &p.HeightCm, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Patient{}, domain.ErrPatientProfileNotFound
	}
	return p, err
}

// GetPatient reads the user's live patient profile.
func (s *Store) GetPatient(ctx context.Context, userID uuid.UUID) (domain.Patient, error) {
	return scanPatient(s.pool.QueryRow(ctx,
		`SELECT `+patientColumns+patientFrom+` WHERE p.user_id = $1 AND p.deleted_at IS NULL`, userID))
}

// enumIDs turns the codes of a patient into the ids of the enumeration tables.
// Unknown or inactive codes are validation errors.
type enumIDs struct {
	gender, diabetesType, therapy *int16
}

func resolveEnums(ctx context.Context, tx pgx.Tx, p domain.Patient) (enumIDs, error) {
	var ids enumIDs
	err := tx.QueryRow(ctx, `
		SELECT (SELECT id FROM genders        WHERE code = $1 AND is_active),
		       (SELECT id FROM diabetes_types WHERE code = $2 AND is_active),
		       (SELECT id FROM therapy_types  WHERE code = $3 AND is_active)`,
		p.Gender, p.DiabetesType, p.Therapy).Scan(&ids.gender, &ids.diabetesType, &ids.therapy)
	if err != nil {
		return ids, err
	}
	var v domain.ValidationError
	if p.Gender != nil && ids.gender == nil {
		v.Add("gender", "unknown value")
	}
	if ids.diabetesType == nil {
		v.Add("diabetes_type", "unknown value")
	}
	if p.Therapy != nil && ids.therapy == nil {
		v.Add("current_therapy", "unknown value")
	}
	return ids, v.OrNil()
}

// CreatePatient creates the patient profile and records the consents, with
// their audit rows, in one transaction (onboarding).
func (s *Store) CreatePatient(ctx context.Context, p domain.Patient, docs []domain.LegalDocument, meta domain.RequestMeta) (domain.Patient, error) {
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		ids, err := resolveEnums(ctx, tx, p)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO patients (user_id, birth_date, gender_id, occupation, diabetes_type_id, therapy_type_id,
			                      target_min_mg_dl, target_max_mg_dl, critical_low_mg_dl, critical_high_mg_dl,
			                      weight_kg, height_cm)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			RETURNING id, created_at, updated_at`,
			p.UserID, p.BirthDate, ids.gender, p.Occupation, ids.diabetesType, ids.therapy,
			p.TargetMin, p.TargetMax, p.CriticalLow, p.CriticalHigh, p.WeightKg, p.HeightCm).
			Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			return err
		}
		for _, d := range docs {
			if _, _, err := acceptConsent(ctx, tx, p.UserID, &p.ID, d, meta); err != nil {
				return err
			}
		}
		return nil
	})
	if database.IsUniqueViolation(err, "uq_patients_user_id") {
		return domain.Patient{}, domain.ErrPatientProfileExists
	}
	if err != nil {
		return domain.Patient{}, err
	}
	return p, nil
}

// UpdatePatient locks the user's live patient profile, lets apply change it
// (apply also validates the result) and saves it.
func (s *Store) UpdatePatient(ctx context.Context, userID uuid.UUID, apply func(*domain.Patient) error) (domain.Patient, error) {
	var p domain.Patient
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		p, err = scanPatient(tx.QueryRow(ctx,
			`SELECT `+patientColumns+patientFrom+` WHERE p.user_id = $1 AND p.deleted_at IS NULL FOR UPDATE OF p`, userID))
		if err != nil {
			return err
		}
		if err := apply(&p); err != nil {
			return err
		}
		ids, err := resolveEnums(ctx, tx, p)
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			UPDATE patients
			   SET birth_date = $2, gender_id = $3, occupation = $4, diabetes_type_id = $5, therapy_type_id = $6,
			       target_min_mg_dl = $7, target_max_mg_dl = $8, critical_low_mg_dl = $9, critical_high_mg_dl = $10,
			       weight_kg = $11, height_cm = $12
			 WHERE id = $1
			RETURNING updated_at`,
			p.ID, p.BirthDate, ids.gender, p.Occupation, ids.diabetesType, ids.therapy,
			p.TargetMin, p.TargetMax, p.CriticalLow, p.CriticalHigh, p.WeightKg, p.HeightCm).
			Scan(&p.UpdatedAt)
	})
	return p, err
}
