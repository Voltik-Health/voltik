package repository

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/voltik/voltik/backend/internal/database"
	"github.com/voltik/voltik/backend/internal/domain"
)

const mealColumns = `
	m.id, m.patient_id, mt.code, m.meal_date, to_char(m.planned_time, 'HH24:MI'), m.status, m.eaten_at,
	m.total_carbs_g, m.total_calories_kcal, m.postprandial_delta_mg_dl, m.notes, m.idempotency_key,
	m.created_at, m.updated_at, m.postprandial_computed_at`

const mealFrom = ` FROM meals m JOIN meal_types mt ON mt.id = m.meal_type_id`

func scanMeal(row pgx.Row) (domain.Meal, error) {
	var m domain.Meal
	err := row.Scan(&m.ID, &m.PatientID, &m.MealType, &m.MealDate, &m.PlannedTime, &m.Status, &m.EatenAt,
		&m.TotalCarbsG, &m.TotalCaloriesKcal, &m.PostprandialDeltaMgDl, &m.Notes, &m.IdempotencyKey,
		&m.CreatedAt, &m.UpdatedAt, &m.PostprandialComputedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, domain.ErrNotFound
	}
	return m, err
}

const photoColumns = `
	p.id, p.meal_id, m.patient_id, p.file_id, p.analysis_status, p.estimated_weight_g, p.estimated_carbs_g,
	p.estimated_calories_kcal, p.detected_items, p.confidence_percent, p.model_version, p.analyzed_at,
	p.confirmed_at, p.analysis_attempts, p.created_at`

func scanPhoto(row pgx.Row) (domain.MealPhoto, error) {
	var p domain.MealPhoto
	var items []byte
	err := row.Scan(&p.ID, &p.MealID, &p.PatientID, &p.FileID, &p.AnalysisStatus, &p.EstimatedWeightG, &p.EstimatedCarbsG,
		&p.EstimatedCaloriesKcal, &items, &p.ConfidencePercent, &p.ModelVersion, &p.AnalyzedAt,
		&p.ConfirmedAt, &p.Attempts, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, domain.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	p.ProposedItems = []domain.ProposedItem{}
	if len(items) > 0 {
		if err := json.Unmarshal(items, &p.ProposedItems); err != nil {
			return p, fmt.Errorf("meal_photos.detected_items: %w", err)
		}
	}
	return p, nil
}

// withMealDetails loads the live items, photos and linked insulin doses of the meals.
func withMealDetails(ctx context.Context, q querier, meals []domain.Meal) error {
	if len(meals) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(meals))
	index := map[uuid.UUID]int{}
	for i, m := range meals {
		ids[i], index[m.ID] = m.ID, i
		meals[i].Items, meals[i].Photos, meals[i].InsulinLogs = []domain.MealItem{}, []domain.MealPhoto{}, []domain.InsulinLog{}
	}
	rows, err := q.Query(ctx, `
		SELECT mi.id, mi.meal_id, s.food_id, f.food_name, mi.serving_id, s.serving_description,
		       mi.serving_quantity, mi.carbs_g, mi.calories_kcal, mi.is_eaten, mi.created_at
		  FROM meal_items mi
		  JOIN food_servings s ON s.id = mi.serving_id
		  JOIN foods f ON f.id = s.food_id
		 WHERE mi.meal_id = ANY($1) AND mi.deleted_at IS NULL
		 ORDER BY mi.meal_id, mi.created_at, mi.id`, ids)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.MealItem, error) {
		var it domain.MealItem
		err := row.Scan(&it.ID, &it.MealID, &it.FoodID, &it.FoodName, &it.ServingID, &it.ServingDescription,
			&it.Quantity, &it.CarbsG, &it.CaloriesKcal, &it.IsEaten, &it.CreatedAt)
		return it, err
	})
	if err != nil {
		return err
	}
	for _, it := range items {
		meals[index[it.MealID]].Items = append(meals[index[it.MealID]].Items, it)
	}
	rows, err = q.Query(ctx, `SELECT `+photoColumns+` FROM meal_photos p JOIN meals m ON m.id = p.meal_id
		 WHERE p.meal_id = ANY($1) AND p.deleted_at IS NULL ORDER BY p.meal_id, p.created_at`, ids)
	if err != nil {
		return err
	}
	photos, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.MealPhoto, error) { return scanPhoto(row) })
	if err != nil {
		return err
	}
	for _, p := range photos {
		meals[index[p.MealID]].Photos = append(meals[index[p.MealID]].Photos, p)
	}
	logs, err := collectInsulin(q.Query(ctx, `SELECT `+insulinColumns+insulinFrom+`
		 WHERE l.meal_id = ANY($1) AND l.deleted_at IS NULL ORDER BY l.administered_at`, ids))
	if err != nil {
		return err
	}
	for _, l := range logs {
		meals[index[*l.MealID]].InsulinLogs = append(meals[index[*l.MealID]].InsulinLogs, l)
	}
	return nil
}

func mealByID(ctx context.Context, q querier, patientID, mealID uuid.UUID, lock bool) (domain.Meal, error) {
	sql := `SELECT ` + mealColumns + mealFrom + ` WHERE m.id = $1 AND m.patient_id = $2 AND m.deleted_at IS NULL`
	if lock {
		sql += ` FOR UPDATE OF m`
	}
	m, err := scanMeal(q.QueryRow(ctx, sql, mealID, patientID))
	if err != nil {
		return m, err
	}
	list := []domain.Meal{m}
	err = withMealDetails(ctx, q, list)
	return list[0], err
}

// GetMeal reads one of the patient's live meals.
func (s *Store) GetMeal(ctx context.Context, patientID, mealID uuid.UUID) (domain.Meal, error) {
	return mealByID(ctx, s.pool, patientID, mealID, false)
}

// ListMeals returns the live meals of the days [from, to], by day, type and time (UC107).
func (s *Store) ListMeals(ctx context.Context, patientID uuid.UUID, from, to time.Time) ([]domain.Meal, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+mealColumns+mealFrom+`
		 WHERE m.patient_id = $1 AND m.deleted_at IS NULL AND m.meal_date BETWEEN $2 AND $3
		 ORDER BY m.meal_date, mt.sort_order,
		          COALESCE(to_char(m.eaten_at AT TIME ZONE 'Europe/Lisbon', 'HH24:MI'), to_char(m.planned_time, 'HH24:MI')),
		          m.created_at`, patientID, from, to)
	if err != nil {
		return nil, err
	}
	meals, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Meal, error) { return scanMeal(row) })
	if err != nil {
		return nil, err
	}
	return meals, withMealDetails(ctx, s.pool, meals)
}

func mealTypeID(ctx context.Context, tx pgx.Tx, code string) (int16, error) {
	var id int16
	err := tx.QueryRow(ctx, `SELECT id FROM meal_types WHERE code = $1 AND is_active`, code).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.Invalid("meal_type", "must be one of the listed values")
	}
	return id, err
}

// frozenItems works out each new item's values from the catalogue: the
// serving's carbohydrates and calories times the quantity (UC101).
func frozenItems(ctx context.Context, q querier, userID uuid.UUID, prefix string, in []domain.MealItemInput) ([]domain.MealItem, error) {
	ids := make([]uuid.UUID, len(in))
	for i, it := range in {
		ids[i] = it.ServingID
	}
	servings, err := visibleServings(ctx, q, userID, ids)
	if err != nil {
		return nil, err
	}
	var v domain.ValidationError
	out := make([]domain.MealItem, len(in))
	for i, it := range in {
		s, ok := servings[it.ServingID]
		if !ok {
			v.Add(fmt.Sprintf("%s[%d].serving_id", prefix, i), "is not a serving in the catalogue")
			continue
		}
		s.Quantity, s.IsEaten = it.Quantity, it.IsEaten
		s.CarbsG, s.CaloriesKcal = domain.Round2(s.CarbsG*it.Quantity), domain.Round2(s.CaloriesKcal*it.Quantity)
		if it.ID != nil {
			s.ID = *it.ID
		}
		out[i] = s
	}
	return out, v.OrNil()
}

func insertItems(ctx context.Context, tx pgx.Tx, mealID uuid.UUID, items []domain.MealItem) error {
	for _, it := range items {
		if _, err := tx.Exec(ctx, `
			INSERT INTO meal_items (meal_id, serving_id, serving_quantity, carbs_g, calories_kcal, is_eaten)
			VALUES ($1, $2, $3, $4, $5, $6)`, mealID, it.ServingID, it.Quantity, it.CarbsG, it.CaloriesKcal, it.IsEaten); err != nil {
			return err
		}
	}
	return nil
}

// recomputeTotals sets the meal's totals to the sum of its live eaten items,
// when it has items. A meal without items keeps the totals typed by hand.
func recomputeTotals(ctx context.Context, tx pgx.Tx, mealID uuid.UUID) error {
	var carbs, kcal float64
	var n int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(sum(carbs_g) FILTER (WHERE is_eaten), 0), COALESCE(sum(calories_kcal) FILTER (WHERE is_eaten), 0), count(*)
		  FROM meal_items WHERE meal_id = $1 AND deleted_at IS NULL`, mealID).Scan(&carbs, &kcal, &n); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	if carbs > domain.MaxMealCarbsG || kcal > domain.MaxMealCaloriesKcal {
		return domain.Invalid("items", "a meal can have at most 2000 g of carbohydrates and 15000 kcal")
	}
	_, err := tx.Exec(ctx, `UPDATE meals SET total_carbs_g = $2, total_calories_kcal = $3 WHERE id = $1`, mealID, carbs, kcal)
	return err
}

// CreateMeal stores a planned or eaten meal with its items in one transaction
// (UC100, UC101). created is false when the idempotency_key was already stored.
func (s *Store) CreateMeal(ctx context.Context, userID uuid.UUID, m domain.Meal, items []domain.MealItemInput) (domain.Meal, bool, error) {
	if m.IdempotencyKey != nil {
		existing, err := s.mealByKey(ctx, m.PatientID, *m.IdempotencyKey)
		if err == nil || !errors.Is(err, domain.ErrNotFound) {
			return existing, false, err
		}
	}
	var id uuid.UUID
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		typeID, err := mealTypeID(ctx, tx, m.MealType)
		if err != nil {
			return err
		}
		frozen, err := frozenItems(ctx, tx, userID, "items", items)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO meals (patient_id, meal_type_id, meal_date, planned_time, status, eaten_at,
			                   total_carbs_g, total_calories_kcal, notes, idempotency_key)
			VALUES ($1, $2, $3, $4::time, $5, $6, $7, $8, $9, $10) RETURNING id`,
			m.PatientID, typeID, m.MealDate, m.PlannedTime, m.Status, m.EatenAt,
			m.TotalCarbsG, m.TotalCaloriesKcal, m.Notes, m.IdempotencyKey).Scan(&id); err != nil {
			return err
		}
		if err := insertItems(ctx, tx, id, frozen); err != nil {
			return err
		}
		return recomputeTotals(ctx, tx, id)
	})
	if database.IsUniqueViolation(err, "meals_idempotency_key_key") {
		existing, err := s.mealByKey(ctx, m.PatientID, *m.IdempotencyKey)
		return existing, false, err
	}
	if err != nil {
		return domain.Meal{}, false, err
	}
	meal, err := s.GetMeal(ctx, m.PatientID, id)
	return meal, true, err
}

func (s *Store) mealByKey(ctx context.Context, patientID uuid.UUID, key string) (domain.Meal, error) {
	var id, owner uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id, patient_id FROM meals WHERE idempotency_key = $1 AND deleted_at IS NULL`, key).Scan(&id, &owner)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Meal{}, domain.ErrNotFound
	case err != nil:
		return domain.Meal{}, err
	case owner != patientID:
		return domain.Meal{}, domain.Invalid("idempotency_key", "is already used")
	}
	return s.GetMeal(ctx, patientID, id)
}

// UpdateMeal lets apply change a meal's details and saves them.
func (s *Store) UpdateMeal(ctx context.Context, patientID, mealID uuid.UUID, apply func(*domain.Meal) error) (domain.Meal, error) {
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		m, err := mealByID(ctx, tx, patientID, mealID, true)
		if err != nil {
			return err
		}
		if err := apply(&m); err != nil {
			return err
		}
		typeID, err := mealTypeID(ctx, tx, m.MealType)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE meals SET meal_type_id = $2, meal_date = $3, planned_time = $4::time, eaten_at = $5, notes = $6,
			       total_carbs_g = $7, total_calories_kcal = $8,
			       postprandial_computed_at = CASE WHEN eaten_at IS DISTINCT FROM $5 THEN NULL ELSE postprandial_computed_at END,
			       postprandial_delta_mg_dl = CASE WHEN eaten_at IS DISTINCT FROM $5 THEN NULL ELSE postprandial_delta_mg_dl END
			 WHERE id = $1`,
			m.ID, typeID, m.MealDate, m.PlannedTime, m.EatenAt, m.Notes, m.TotalCarbsG, m.TotalCaloriesKcal)
		return err
	})
	if err != nil {
		return domain.Meal{}, err
	}
	return s.GetMeal(ctx, patientID, mealID)
}

// ReplaceMealItems applies the full list of items of a meal (UC102): items
// with an id are kept and updated, new ones are added, the others are
// removed logically. Items whose serving and quantity did not change keep
// their frozen values.
func (s *Store) ReplaceMealItems(ctx context.Context, userID, patientID, mealID uuid.UUID, items []domain.MealItemInput) (domain.Meal, error) {
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		m, err := mealByID(ctx, tx, patientID, mealID, true)
		if err != nil {
			return err
		}
		current := map[uuid.UUID]domain.MealItem{}
		for _, it := range m.Items {
			current[it.ID] = it
		}
		var v domain.ValidationError
		for i, it := range items {
			if it.ID != nil {
				if _, ok := current[*it.ID]; !ok {
					v.Add(fmt.Sprintf("items[%d].id", i), "is not an item of this meal")
				}
			}
		}
		if err := v.OrNil(); err != nil {
			return err
		}
		frozen, err := frozenItems(ctx, tx, userID, "items", items)
		if err != nil {
			return err
		}
		keep := map[uuid.UUID]bool{}
		var added []domain.MealItem
		for i, it := range items {
			if it.ID == nil {
				added = append(added, frozen[i])
				continue
			}
			keep[*it.ID] = true
			old := current[*it.ID]
			carbs, kcal := old.CarbsG, old.CaloriesKcal
			if old.ServingID != it.ServingID || old.Quantity != it.Quantity {
				carbs, kcal = frozen[i].CarbsG, frozen[i].CaloriesKcal
			}
			if _, err := tx.Exec(ctx, `
				UPDATE meal_items SET serving_id = $2, serving_quantity = $3, carbs_g = $4, calories_kcal = $5, is_eaten = $6
				 WHERE id = $1`, *it.ID, it.ServingID, it.Quantity, carbs, kcal, it.IsEaten); err != nil {
				return err
			}
		}
		for id := range current {
			if !keep[id] {
				if _, err := tx.Exec(ctx, `UPDATE meal_items SET deleted_at = now() WHERE id = $1`, id); err != nil {
					return err
				}
			}
		}
		if err := insertItems(ctx, tx, mealID, added); err != nil {
			return err
		}
		if len(items) == 0 && len(current) > 0 {
			_, err := tx.Exec(ctx, `UPDATE meals SET total_carbs_g = 0, total_calories_kcal = 0 WHERE id = $1`, mealID)
			return err
		}
		return recomputeTotals(ctx, tx, mealID)
	})
	if err != nil {
		return domain.Meal{}, err
	}
	return s.GetMeal(ctx, patientID, mealID)
}

// MarkMealEaten confirms a planned meal (UC101 A1). An eaten meal is left as it is.
func (s *Store) MarkMealEaten(ctx context.Context, patientID, mealID uuid.UUID, eatenAt time.Time) (domain.Meal, bool, error) {
	changed := false
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		m, err := mealByID(ctx, tx, patientID, mealID, true)
		if err != nil {
			return err
		}
		switch m.Status {
		case domain.MealEaten:
			return nil
		case domain.MealCancelled:
			return domain.InvalidState("A cancelled meal cannot be marked as eaten.")
		}
		changed = true
		_, err = tx.Exec(ctx, `UPDATE meals SET status = 'eaten', eaten_at = $2 WHERE id = $1`, mealID, eatenAt)
		return err
	})
	if err != nil {
		return domain.Meal{}, false, err
	}
	m, err := s.GetMeal(ctx, patientID, mealID)
	return m, changed, err
}

// CancelMeal cancels a planned meal (UC106).
func (s *Store) CancelMeal(ctx context.Context, patientID, mealID uuid.UUID) (domain.Meal, error) {
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		m, err := mealByID(ctx, tx, patientID, mealID, true)
		if err != nil {
			return err
		}
		switch m.Status {
		case domain.MealCancelled:
			return nil
		case domain.MealEaten:
			return domain.InvalidState("An eaten meal cannot be cancelled. Delete it instead.")
		}
		_, err = tx.Exec(ctx, `UPDATE meals SET status = 'cancelled' WHERE id = $1`, mealID)
		return err
	})
	if err != nil {
		return domain.Meal{}, err
	}
	return s.GetMeal(ctx, patientID, mealID)
}

// DeleteMeal deletes a meal, its items and its photos logically (UC106).
// Linked insulin doses stay.
func (s *Store) DeleteMeal(ctx context.Context, patientID, mealID uuid.UUID) error {
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE meals SET deleted_at = now() WHERE id = $1 AND patient_id = $2 AND deleted_at IS NULL`,
			mealID, patientID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		for _, sql := range []string{
			`UPDATE meal_items SET deleted_at = now() WHERE meal_id = $1 AND deleted_at IS NULL`,
			`UPDATE meal_photos SET deleted_at = now() WHERE meal_id = $1 AND deleted_at IS NULL`,
		} {
			if _, err := tx.Exec(ctx, sql, mealID); err != nil {
				return err
			}
		}
		return nil
	})
}

// --- Files -------------------------------------------------------------------

func insertFile(ctx context.Context, tx pgx.Tx, f domain.StoredFile) (uuid.UUID, error) {
	sum := sha256.Sum256(f.Data)
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO stored_files (patient_id, purpose, content_type, size_bytes, sha256, data)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		f.PatientID, f.Purpose, f.ContentType, len(f.Data), sum[:], f.Data).Scan(&id)
	return id, err
}

// GetFile reads a stored file with its bytes.
func (s *Store) GetFile(ctx context.Context, id uuid.UUID) (domain.StoredFile, error) {
	var f domain.StoredFile
	err := s.pool.QueryRow(ctx, `
		SELECT f.id, f.patient_id, f.purpose, f.content_type, f.data, f.created_at
		  FROM stored_files f JOIN patients p ON p.id = f.patient_id AND p.deleted_at IS NULL
		 WHERE f.id = $1`, id).Scan(&f.ID, &f.PatientID, &f.Purpose, &f.ContentType, &f.Data, &f.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, domain.ErrNotFound
	}
	return f, err
}

// --- Meal photos (UC103–UC105) ---------------------------------------------------

// CreateMealPhoto stores the image and a pending photo of the meal.
func (s *Store) CreateMealPhoto(ctx context.Context, patientID, mealID uuid.UUID, file domain.StoredFile) (domain.MealPhoto, error) {
	var photoID uuid.UUID
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := mealByID(ctx, tx, patientID, mealID, true); err != nil {
			return err
		}
		file.PatientID = patientID
		fileID, err := insertFile(ctx, tx, file)
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO meal_photos (meal_id, image_url, file_id) VALUES ($1, $2, $3) RETURNING id`,
			mealID, "/v1/files/"+fileID.String(), fileID).Scan(&photoID)
	})
	if err != nil {
		return domain.MealPhoto{}, err
	}
	return s.GetMealPhoto(ctx, patientID, mealID, photoID)
}

// GetMealPhoto reads a live photo of one of the patient's meals.
func (s *Store) GetMealPhoto(ctx context.Context, patientID, mealID, photoID uuid.UUID) (domain.MealPhoto, error) {
	return scanPhoto(s.pool.QueryRow(ctx, `SELECT `+photoColumns+` FROM meal_photos p JOIN meals m ON m.id = p.meal_id
		 WHERE p.id = $1 AND p.meal_id = $2 AND m.patient_id = $3 AND p.deleted_at IS NULL AND m.deleted_at IS NULL`,
		photoID, mealID, patientID))
}

// ClaimPhotoAnalyses takes up to limit pending photos to analyse. Each claim
// counts as an attempt and is leased for 2 minutes.
func (s *Store) ClaimPhotoAnalyses(ctx context.Context, now time.Time, limit int) ([]domain.MealPhoto, error) {
	rows, err := s.pool.Query(ctx, `
		WITH claimed AS (
		    UPDATE meal_photos p SET analysis_attempts = p.analysis_attempts + 1
		     WHERE p.id IN (SELECT id FROM meal_photos
		                     WHERE analysis_status = 'pending' AND deleted_at IS NULL AND analysis_attempts < $3
		                       AND (analysis_attempts = 0 OR updated_at < $1::timestamptz - interval '2 minutes')
		                     ORDER BY created_at LIMIT $2 FOR UPDATE SKIP LOCKED)
		    RETURNING p.id)
		SELECT `+photoColumns+` FROM meal_photos p JOIN meals m ON m.id = p.meal_id JOIN claimed c ON c.id = p.id`,
		now, limit, domain.MaxPhotoAttempts)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.MealPhoto, error) { return scanPhoto(row) })
}

// CompletePhotoAnalysis saves the estimate of a photo (UC104).
func (s *Store) CompletePhotoAnalysis(ctx context.Context, photoID uuid.UUID, a domain.PhotoAnalysis) error {
	items, err := json.Marshal(a.Items)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE meal_photos SET analysis_status = 'completed', analyzed_at = now(), detected_items = $2,
		       estimated_weight_g = $3, estimated_carbs_g = $4, estimated_calories_kcal = $5,
		       confidence_percent = $6, model_version = $7
		 WHERE id = $1 AND analysis_status = 'pending'`,
		photoID, items, a.WeightG, a.CarbsG, a.CaloriesKcal, a.ConfidencePercent, a.ModelVersion)
	return err
}

// FailPhotoAnalysis marks the analysis failed: the patient logs the meal by hand (UC104 A1).
func (s *Store) FailPhotoAnalysis(ctx context.Context, photoID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE meal_photos SET analysis_status = 'failed' WHERE id = $1 AND analysis_status = 'pending'`, photoID)
	return err
}

// ConfirmMealPhoto adds the items the patient accepted from a photo to the
// meal and marks the photo confirmed (UC105).
func (s *Store) ConfirmMealPhoto(ctx context.Context, userID, patientID, mealID, photoID uuid.UUID, items []domain.MealItemInput) (domain.Meal, error) {
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := mealByID(ctx, tx, patientID, mealID, true); err != nil {
			return err
		}
		p, err := scanPhoto(tx.QueryRow(ctx, `SELECT `+photoColumns+` FROM meal_photos p JOIN meals m ON m.id = p.meal_id
			 WHERE p.id = $1 AND p.meal_id = $2 AND p.deleted_at IS NULL FOR UPDATE OF p`, photoID, mealID))
		if err != nil {
			return err
		}
		switch {
		case p.AnalysisStatus == domain.PhotoPending:
			return domain.ErrAnalysisNotReady
		case p.AnalysisStatus == domain.PhotoFailed:
			return domain.InvalidState("The analysis of this photo failed. Log the meal by hand.")
		case p.ConfirmedAt != nil:
			return domain.InvalidState("This photo was already confirmed.")
		}
		frozen, err := frozenItems(ctx, tx, userID, "items", items)
		if err != nil {
			return err
		}
		if err := insertItems(ctx, tx, mealID, frozen); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE meal_photos SET confirmed_at = now() WHERE id = $1`, photoID); err != nil {
			return err
		}
		return recomputeTotals(ctx, tx, mealID)
	})
	if err != nil {
		return domain.Meal{}, err
	}
	return s.GetMeal(ctx, patientID, mealID)
}

// --- Post-meal impact (UC108) ---------------------------------------------------

// MealsDueForImpact are eaten meals whose 2 hours after have passed and whose
// impact was not computed yet (at most 30 days old).
func (s *Store) MealsDueForImpact(ctx context.Context, now time.Time, limit int) ([]domain.Meal, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+mealColumns+mealFrom+`
		 WHERE m.status = 'eaten' AND m.postprandial_computed_at IS NULL AND m.deleted_at IS NULL
		   AND m.eaten_at <= $1 AND m.eaten_at > $2
		 ORDER BY m.eaten_at LIMIT $3`, now.Add(-2*time.Hour), now.Add(-30*24*time.Hour), limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Meal, error) { return scanMeal(row) })
}

// SetPostprandial saves the impact of a meal; nil when there were not enough readings.
func (s *Store) SetPostprandial(ctx context.Context, mealID uuid.UUID, delta *int) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE meals SET postprandial_delta_mg_dl = $2, postprandial_computed_at = now() WHERE id = $1`, mealID, delta)
	return err
}
