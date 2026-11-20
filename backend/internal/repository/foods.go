package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/voltik/voltik/backend/internal/database"
	"github.com/voltik/voltik/backend/internal/domain"
)

const foodColumns = `
	f.id, f.food_name, f.brand, f.food_type, f.data_source, f.category_id, fc.name, f.barcode_ean,
	f.is_verified, f.created_by_user_id, f.created_at, f.updated_at`

const foodFrom = ` FROM foods f LEFT JOIN food_categories fc ON fc.id = f.category_id`

// A food created by a user is visible only to them until it is verified (UC093, UC095).
const foodVisible = `(f.is_verified OR f.data_source <> 'user_custom' OR f.created_by_user_id = $1)`

func scanFood(row pgx.Row) (domain.Food, error) {
	var f domain.Food
	var categoryID *uuid.UUID
	var categoryName *string
	err := row.Scan(&f.ID, &f.Name, &f.Brand, &f.Type, &f.DataSource, &categoryID, &categoryName, &f.BarcodeEAN,
		&f.IsVerified, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, domain.ErrNotFound
	}
	if categoryID != nil && categoryName != nil {
		f.Category = &domain.FoodCategoryRef{ID: *categoryID, Name: *categoryName}
	}
	return f, err
}

const servingColumns = `s.id, s.food_id, s.serving_description, s.metric_amount, s.metric_unit,
	s.carbs_g, s.fiber_g, s.sugars_g, s.calories_kcal, s.protein_g, s.fat_g, s.saturated_fat_g, s.sodium_mg`

func scanServing(row pgx.Row) (domain.FoodServing, error) {
	var s domain.FoodServing
	err := row.Scan(&s.ID, &s.FoodID, &s.Description, &s.MetricAmount, &s.MetricUnit,
		&s.CarbsG, &s.FiberG, &s.SugarsG, &s.CaloriesKcal, &s.ProteinG, &s.FatG, &s.SaturatedFatG, &s.SodiumMg)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, domain.ErrNotFound
	}
	return s, err
}

// withFoodDetails loads the live servings and the ingredients of the foods.
func withFoodDetails(ctx context.Context, q querier, foods []domain.Food) error {
	if len(foods) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(foods))
	index := map[uuid.UUID]int{}
	for i, f := range foods {
		ids[i], index[f.ID] = f.ID, i
		foods[i].Servings, foods[i].Components = []domain.FoodServing{}, []domain.FoodComponent{}
	}
	rows, err := q.Query(ctx, `SELECT `+servingColumns+` FROM food_servings s
		 WHERE s.food_id = ANY($1) AND s.deleted_at IS NULL
		 ORDER BY s.food_id, s.metric_amount NULLS LAST, s.serving_description`, ids)
	if err != nil {
		return err
	}
	servings, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.FoodServing, error) { return scanServing(row) })
	if err != nil {
		return err
	}
	for _, s := range servings {
		i := index[s.FoodID]
		foods[i].Servings = append(foods[i].Servings, s)
	}
	rows, err = q.Query(ctx, `
		SELECT c.dish_food_id, c.id, c.component_food_id, f.food_name, c.default_quantity_g
		  FROM food_components c JOIN foods f ON f.id = c.component_food_id
		 WHERE c.dish_food_id = ANY($1) ORDER BY c.dish_food_id, c.created_at, c.id`, ids)
	if err != nil {
		return err
	}
	for rows.Next() {
		var dish uuid.UUID
		var c domain.FoodComponent
		if err := rows.Scan(&dish, &c.ID, &c.ComponentFoodID, &c.FoodName, &c.DefaultGrams); err != nil {
			rows.Close()
			return err
		}
		i := index[dish]
		foods[i].Components = append(foods[i].Components, c)
	}
	return rows.Err()
}

func collectFoods(ctx context.Context, q querier, rows pgx.Rows, err error) ([]domain.Food, error) {
	if err != nil {
		return nil, err
	}
	foods, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Food, error) { return scanFood(row) })
	if err != nil {
		return nil, err
	}
	return foods, withFoodDetails(ctx, q, foods)
}

// likeSafe removes the characters LIKE treats as wildcards.
var likeSafe = strings.NewReplacer("%", " ", "_", " ", `\`, " ")

// SearchFoods finds visible foods whose name has every word of q, ignoring
// accents and case (UC093). Names that start with q come first, then the
// verified foods, then the composite dishes, then the shorter names.
func (s *Store) SearchFoods(ctx context.Context, userID uuid.UUID, q string, limit int) ([]domain.Food, error) {
	q = strings.Join(strings.Fields(likeSafe.Replace(q)), " ")
	if q == "" {
		return []domain.Food{}, nil
	}
	rows, err := s.pool.Query(ctx, `
		WITH q AS (SELECT voltik_search_key($2) AS key)
		SELECT `+foodColumns+foodFrom+`, q
		 WHERE f.deleted_at IS NULL AND `+foodVisible+`
		   AND NOT EXISTS (
		       SELECT 1 FROM regexp_split_to_table(q.key, '\s+') AS w (word)
		        WHERE w.word <> '' AND voltik_search_key(f.food_name) NOT LIKE '%' || w.word || '%')
		 ORDER BY (voltik_search_key(f.food_name) LIKE q.key || '%') DESC, f.is_verified DESC,
		          (f.food_type = 'composite_dish') DESC, length(f.food_name), f.food_name
		 LIMIT $3`, userID, q, limit)
	return collectFoods(ctx, s.pool, rows, err)
}

// GetFood reads a visible food with its servings and ingredients.
func (s *Store) GetFood(ctx context.Context, userID, foodID uuid.UUID) (domain.Food, error) {
	return getFood(ctx, s.pool, `WHERE f.id = $2 AND f.deleted_at IS NULL AND `+foodVisible, userID, foodID)
}

// FoodByBarcode finds a visible live food by its barcode.
func (s *Store) FoodByBarcode(ctx context.Context, userID uuid.UUID, ean string) (domain.Food, error) {
	return getFood(ctx, s.pool, `WHERE f.barcode_ean = $2 AND f.deleted_at IS NULL AND `+foodVisible, userID, ean)
}

func getFood(ctx context.Context, q querier, where string, args ...any) (domain.Food, error) {
	f, err := scanFood(q.QueryRow(ctx, `SELECT `+foodColumns+foodFrom+` `+where, args...))
	if err != nil {
		return f, err
	}
	list := []domain.Food{f}
	err = withFoodDetails(ctx, q, list)
	return list[0], err
}

// CreateFood stores a food and its servings in one transaction (UC095, the
// Open Food Facts products and the admin catalogue).
func (s *Store) CreateFood(ctx context.Context, f domain.Food) (domain.Food, error) {
	var id uuid.UUID
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var categoryID *uuid.UUID
		if f.Category != nil {
			categoryID = &f.Category.ID
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO foods (food_name, brand, food_type, data_source, category_id, barcode_ean, is_verified, created_by_user_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
			f.Name, f.Brand, f.Type, f.DataSource, categoryID, f.BarcodeEAN, f.IsVerified, f.CreatedBy).Scan(&id)
		if err != nil {
			return err
		}
		for i, sv := range f.Servings {
			if err := insertServing(ctx, tx, id, sv, fmt.Sprintf("servings[%d].", i)); err != nil {
				return err
			}
		}
		return nil
	})
	switch {
	case database.IsUniqueViolation(err, "uq_foods_barcode_ean"):
		return domain.Food{}, domain.ErrBarcodeInUse
	case database.IsForeignKeyViolation(err):
		return domain.Food{}, domain.Invalid("category_id", "is not a known category")
	case err != nil:
		return domain.Food{}, err
	}
	return getFood(ctx, s.pool, `WHERE f.id = $1`, id)
}

func insertServing(ctx context.Context, tx pgx.Tx, foodID uuid.UUID, sv domain.FoodServing, prefix string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO food_servings (food_id, serving_description, metric_amount, metric_unit, carbs_g, fiber_g, sugars_g,
		                           calories_kcal, protein_g, fat_g, saturated_fat_g, sodium_mg)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		foodID, strings.TrimSpace(sv.Description), sv.MetricAmount, sv.MetricUnit, sv.CarbsG, sv.FiberG, sv.SugarsG,
		sv.CaloriesKcal, sv.ProteinG, sv.FatG, sv.SaturatedFatG, sv.SodiumMg)
	if database.IsUniqueViolation(err, "uq_food_serving_desc") {
		return domain.Invalid(prefix+"serving_description", "the food already has a serving with this description")
	}
	return err
}

// ListFoodCategories lists the categories with the number of foods everyone can see.
func (s *Store) ListFoodCategories(ctx context.Context) ([]domain.FoodCategory, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT fc.id, fc.name,
		       count(f.id) FILTER (WHERE f.deleted_at IS NULL AND (f.is_verified OR f.data_source <> 'user_custom'))::int,
		       fc.created_at, fc.updated_at
		  FROM food_categories fc LEFT JOIN foods f ON f.category_id = fc.id
		 GROUP BY fc.id ORDER BY fc.name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanCategory)
}

func scanCategory(row pgx.CollectableRow) (domain.FoodCategory, error) {
	var c domain.FoodCategory
	err := row.Scan(&c.ID, &c.Name, &c.FoodCount, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func (s *Store) category(ctx context.Context, id uuid.UUID) (domain.FoodCategory, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT fc.id, fc.name,
		       count(f.id) FILTER (WHERE f.deleted_at IS NULL AND (f.is_verified OR f.data_source <> 'user_custom'))::int,
		       fc.created_at, fc.updated_at
		  FROM food_categories fc LEFT JOIN foods f ON f.category_id = fc.id
		 WHERE fc.id = $1 GROUP BY fc.id`, id)
	if err != nil {
		return domain.FoodCategory{}, err
	}
	c, err := pgx.CollectExactlyOneRow(rows, scanCategory)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, domain.ErrNotFound
	}
	return c, err
}

// CreateFoodCategory adds a category (UC096).
func (s *Store) CreateFoodCategory(ctx context.Context, name string) (domain.FoodCategory, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `INSERT INTO food_categories (name) VALUES ($1) RETURNING id`, name).Scan(&id)
	if database.IsUniqueViolation(err, "food_categories_name_key") {
		return domain.FoodCategory{}, domain.ErrCategoryNameTaken
	}
	if err != nil {
		return domain.FoodCategory{}, err
	}
	return s.category(ctx, id)
}

// RenameFoodCategory renames a category (UC096).
func (s *Store) RenameFoodCategory(ctx context.Context, id uuid.UUID, name string) (domain.FoodCategory, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE food_categories SET name = $2 WHERE id = $1`, id, name)
	switch {
	case database.IsUniqueViolation(err, "food_categories_name_key"):
		return domain.FoodCategory{}, domain.ErrCategoryNameTaken
	case err != nil:
		return domain.FoodCategory{}, err
	case tag.RowsAffected() == 0:
		return domain.FoodCategory{}, domain.ErrNotFound
	}
	return s.category(ctx, id)
}

// DeleteFoodCategory deletes a category; its foods stay, without one (ON DELETE SET NULL).
func (s *Store) DeleteFoodCategory(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM food_categories WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

// lockFood locks a live food for the admin operations.
func lockFood(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Food, error) {
	f, err := scanFood(tx.QueryRow(ctx, `SELECT `+foodColumns+foodFrom+` WHERE f.id = $1 AND f.deleted_at IS NULL FOR UPDATE OF f`, id))
	if err != nil {
		return f, err
	}
	list := []domain.Food{f}
	err = withFoodDetails(ctx, tx, list)
	return list[0], err
}

// UpdateFood lets apply correct the name, brand, barcode or category of a food.
func (s *Store) UpdateFood(ctx context.Context, id uuid.UUID, apply func(*domain.Food) error) (domain.Food, error) {
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		f, err := lockFood(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := apply(&f); err != nil {
			return err
		}
		var categoryID *uuid.UUID
		if f.Category != nil {
			categoryID = &f.Category.ID
		}
		_, err = tx.Exec(ctx, `UPDATE foods SET food_name = $2, brand = $3, barcode_ean = $4, category_id = $5 WHERE id = $1`,
			id, f.Name, f.Brand, f.BarcodeEAN, categoryID)
		return err
	})
	switch {
	case database.IsUniqueViolation(err, "uq_foods_barcode_ean"):
		return domain.Food{}, domain.ErrBarcodeInUse
	case database.IsForeignKeyViolation(err):
		return domain.Food{}, domain.Invalid("category_id", "is not a known category")
	case err != nil:
		return domain.Food{}, err
	}
	return getFood(ctx, s.pool, `WHERE f.id = $1`, id)
}

// DeleteFood removes a food from the catalogue logically. Past meals keep it.
func (s *Store) DeleteFood(ctx context.Context, id uuid.UUID) error {
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := lockFood(ctx, tx, id); err != nil {
			return err
		}
		var used bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM food_components c JOIN foods d ON d.id = c.dish_food_id
			                WHERE c.component_food_id = $1 AND d.deleted_at IS NULL)`, id).Scan(&used); err != nil {
			return err
		}
		if used {
			return domain.InvalidState("This food is an ingredient of a dish. Remove it from the dish first.")
		}
		_, err := tx.Exec(ctx, `UPDATE foods SET deleted_at = now() WHERE id = $1`, id)
		return err
	})
}

// VerifyFood marks a food as reviewed (UC099).
func (s *Store) VerifyFood(ctx context.Context, id uuid.UUID) (domain.Food, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE foods SET is_verified = TRUE WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return domain.Food{}, err
	}
	if tag.RowsAffected() == 0 {
		return domain.Food{}, domain.ErrNotFound
	}
	return getFood(ctx, s.pool, `WHERE f.id = $1`, id)
}

// AddFoodComponent adds an ingredient to a composite dish and recomputes its
// servings (UC097).
func (s *Store) AddFoodComponent(ctx context.Context, dishID, componentID uuid.UUID, grams float64) (domain.Food, error) {
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		dish, err := lockFood(ctx, tx, dishID)
		if err != nil {
			return err
		}
		if dish.Type != domain.FoodCompositeDish {
			return domain.InvalidState("Only composite dishes have ingredients.")
		}
		if componentID == dishID {
			return domain.Invalid("component_food_id", "a dish cannot be its own ingredient")
		}
		var hasGrams bool
		err = tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM foods f JOIN food_servings s ON s.food_id = f.id
			                WHERE f.id = $1 AND f.deleted_at IS NULL AND s.deleted_at IS NULL
			                  AND s.metric_unit = 'g' AND s.metric_amount > 0)`, componentID).Scan(&hasGrams)
		if err != nil {
			return err
		}
		if !hasGrams {
			return domain.Invalid("component_food_id", "must be a food with a serving measured in grams")
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO food_components (dish_food_id, component_food_id, default_quantity_g) VALUES ($1, $2, $3)`,
			dishID, componentID, grams)
		if database.IsUniqueViolation(err, "uq_dish_component") {
			return domain.ErrComponentExists
		}
		if err != nil {
			return err
		}
		return recomputeDish(ctx, tx, dishID)
	})
	if err != nil {
		return domain.Food{}, err
	}
	return getFood(ctx, s.pool, `WHERE f.id = $1`, dishID)
}

// RemoveFoodComponent removes an ingredient and recomputes the dish's servings.
func (s *Store) RemoveFoodComponent(ctx context.Context, dishID, componentRowID uuid.UUID) (domain.Food, error) {
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := lockFood(ctx, tx, dishID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM food_components WHERE id = $1 AND dish_food_id = $2`, componentRowID, dishID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		return recomputeDish(ctx, tx, dishID)
	})
	if err != nil {
		return domain.Food{}, err
	}
	return getFood(ctx, s.pool, `WHERE f.id = $1`, dishID)
}

// recomputeDish replaces the servings of a dish with "1 prato (N g)" and
// "100 g", worked out from its ingredients and their gram servings. Meals
// already logged keep the servings they used.
func recomputeDish(ctx context.Context, tx pgx.Tx, dishID uuid.UUID) error {
	rows, err := tx.Query(ctx, `
		SELECT c.default_quantity_g / s.metric_amount,
		       s.carbs_g, COALESCE(s.fiber_g, 0), COALESCE(s.sugars_g, 0), s.calories_kcal, s.protein_g, s.fat_g,
		       COALESCE(s.saturated_fat_g, 0), COALESCE(s.sodium_mg, 0), c.default_quantity_g
		  FROM food_components c
		  JOIN LATERAL (SELECT * FROM food_servings s
		                 WHERE s.food_id = c.component_food_id AND s.deleted_at IS NULL
		                   AND s.metric_unit = 'g' AND s.metric_amount > 0
		                 ORDER BY (s.metric_amount = 100) DESC, s.metric_amount LIMIT 1) s ON TRUE
		 WHERE c.dish_food_id = $1`, dishID)
	if err != nil {
		return err
	}
	var total domain.Nutrition
	var fiber, sugars, saturated, sodium, grams float64
	for rows.Next() {
		var factor, carbs, fib, sug, kcal, prot, fat, sat, na, g float64
		if err := rows.Scan(&factor, &carbs, &fib, &sug, &kcal, &prot, &fat, &sat, &na, &g); err != nil {
			rows.Close()
			return err
		}
		total.CarbsG += carbs * factor
		fiber += fib * factor
		sugars += sug * factor
		total.CaloriesKcal += kcal * factor
		total.ProteinG += prot * factor
		total.FatG += fat * factor
		saturated += sat * factor
		sodium += na * factor
		grams += g
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE food_servings SET deleted_at = now() WHERE food_id = $1 AND deleted_at IS NULL`, dishID); err != nil {
		return err
	}
	if grams <= 0 {
		return nil
	}
	scale := func(per float64) domain.FoodServing {
		f := per / grams
		amount := domain.Round2(per)
		unit := "g"
		return domain.FoodServing{
			MetricAmount: &amount, MetricUnit: &unit,
			Nutrition: domain.Nutrition{
				CarbsG: domain.Round2(total.CarbsG * f), FiberG: ptr(domain.Round2(fiber * f)),
				SugarsG: ptr(domain.Round2(sugars * f)), CaloriesKcal: domain.Round2(total.CaloriesKcal * f),
				ProteinG: domain.Round2(total.ProteinG * f), FatG: domain.Round2(total.FatG * f),
				SaturatedFatG: ptr(domain.Round2(saturated * f)), SodiumMg: ptr(domain.Round2(sodium * f)),
			},
		}
	}
	plate := scale(grams)
	plate.Description = fmt.Sprintf("1 prato (%s g)", strconv.FormatFloat(domain.Round2(grams), 'f', -1, 64))
	hundred := scale(100)
	hundred.Description = "100 g"
	for _, sv := range []domain.FoodServing{plate, hundred} {
		if domain.ValidateServing(sv) != nil {
			continue // a very large dish: its plate is above the limits of a serving
		}
		if err := insertServing(ctx, tx, dishID, sv, ""); err != nil {
			return err
		}
	}
	return nil
}

func ptr[T any](v T) *T { return &v }

func hasComponents(ctx context.Context, tx pgx.Tx, foodID uuid.UUID) (bool, error) {
	var has bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM food_components WHERE dish_food_id = $1)`, foodID).Scan(&has)
	return has, err
}

var errComputedServings = domain.InvalidState("The servings of a dish with ingredients are worked out from them.")

// AddFoodServing adds a serving to a food (UC098).
func (s *Store) AddFoodServing(ctx context.Context, foodID uuid.UUID, sv domain.FoodServing) (domain.FoodServing, error) {
	var id uuid.UUID
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := lockFood(ctx, tx, foodID); err != nil {
			return err
		}
		if has, err := hasComponents(ctx, tx, foodID); err != nil || has {
			if err == nil {
				err = errComputedServings
			}
			return err
		}
		if err := insertServing(ctx, tx, foodID, sv, ""); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT id FROM food_servings WHERE food_id = $1 AND serving_description = $2 AND deleted_at IS NULL`,
			foodID, strings.TrimSpace(sv.Description)).Scan(&id)
	})
	if err != nil {
		return domain.FoodServing{}, err
	}
	return scanServing(s.pool.QueryRow(ctx, `SELECT `+servingColumns+` FROM food_servings s WHERE s.id = $1`, id))
}

// UpdateFoodServing lets apply correct a serving (UC098). Past meal items
// keep their values.
func (s *Store) UpdateFoodServing(ctx context.Context, foodID, servingID uuid.UUID, apply func(*domain.FoodServing) error) (domain.FoodServing, error) {
	var sv domain.FoodServing
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := lockFood(ctx, tx, foodID); err != nil {
			return err
		}
		if has, err := hasComponents(ctx, tx, foodID); err != nil || has {
			if err == nil {
				err = errComputedServings
			}
			return err
		}
		var err error
		sv, err = scanServing(tx.QueryRow(ctx, `SELECT `+servingColumns+` FROM food_servings s
			 WHERE s.id = $1 AND s.food_id = $2 AND s.deleted_at IS NULL FOR UPDATE`, servingID, foodID))
		if err != nil {
			return err
		}
		if err := apply(&sv); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE food_servings SET serving_description = $2, metric_amount = $3, metric_unit = $4, carbs_g = $5,
			       fiber_g = $6, sugars_g = $7, calories_kcal = $8, protein_g = $9, fat_g = $10,
			       saturated_fat_g = $11, sodium_mg = $12
			 WHERE id = $1`,
			sv.ID, strings.TrimSpace(sv.Description), sv.MetricAmount, sv.MetricUnit, sv.CarbsG, sv.FiberG, sv.SugarsG,
			sv.CaloriesKcal, sv.ProteinG, sv.FatG, sv.SaturatedFatG, sv.SodiumMg)
		if database.IsUniqueViolation(err, "uq_food_serving_desc") {
			return domain.Invalid("serving_description", "the food already has a serving with this description")
		}
		return err
	})
	return sv, err
}

// DeleteFoodServing removes a serving logically (UC098).
func (s *Store) DeleteFoodServing(ctx context.Context, foodID, servingID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE food_servings s SET deleted_at = now()
		  FROM foods f WHERE f.id = s.food_id AND f.deleted_at IS NULL
		   AND s.id = $1 AND s.food_id = $2 AND s.deleted_at IS NULL`, servingID, foodID)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

// FoodsForReview lists live foods for the review queue, most used first (UC099).
func (s *Store) FoodsForReview(ctx context.Context, verified *bool, q *string, limit int, cursor string) ([]domain.Food, string, error) {
	offset := 0
	if cursor != "" {
		_, raw, err := decodeCursor(cursor)
		if err == nil {
			offset, err = strconv.Atoi(raw)
		}
		if err != nil || offset < 0 {
			return nil, "", domain.Invalid("cursor", "is not a cursor returned by the API")
		}
	}
	var search *string
	if q != nil {
		cleaned := strings.Join(strings.Fields(likeSafe.Replace(*q)), " ")
		search = &cleaned
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+foodColumns+`,
		       (SELECT count(*) FROM meal_items mi JOIN food_servings sv ON sv.id = mi.serving_id
		         WHERE sv.food_id = f.id AND mi.deleted_at IS NULL)::int AS usage
		  `+foodFrom+`
		 WHERE f.deleted_at IS NULL
		   AND ($1::boolean IS NULL OR f.is_verified = $1)
		   AND ($2::text IS NULL OR voltik_search_key(f.food_name) LIKE '%' || voltik_search_key($2) || '%')
		 ORDER BY usage DESC, f.created_at, f.id
		 OFFSET $3 LIMIT $4`, verified, search, offset, limit+1)
	if err != nil {
		return nil, "", err
	}
	foods, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Food, error) {
		var f domain.Food
		var categoryID *uuid.UUID
		var categoryName *string
		err := row.Scan(&f.ID, &f.Name, &f.Brand, &f.Type, &f.DataSource, &categoryID, &categoryName, &f.BarcodeEAN,
			&f.IsVerified, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt, &f.UsageCount)
		if categoryID != nil && categoryName != nil {
			f.Category = &domain.FoodCategoryRef{ID: *categoryID, Name: *categoryName}
		}
		return f, err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(foods) > limit {
		foods = foods[:limit]
		next = encodeCursor(time.Unix(0, 0), strconv.Itoa(offset+limit))
	}
	return foods, next, withFoodDetails(ctx, s.pool, foods)
}

// IsNutritionReviewer reports whether the user is a clinician who may verify foods (UC099).
func (s *Store) IsNutritionReviewer(ctx context.Context, userID uuid.UUID) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM health_professionals h JOIN medical_specialties m ON m.id = h.specialty_id
		                WHERE h.user_id = $1 AND h.deleted_at IS NULL
		                  AND m.code IN ('clinical_nutrition', 'endocrinology'))`, userID).Scan(&ok)
	return ok, err
}

// visibleServings returns the servings of the ids that the user can log,
// with their food.
func visibleServings(ctx context.Context, q querier, userID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]domain.MealItem, error) {
	rows, err := q.Query(ctx, `
		SELECT s.id, s.food_id, f.food_name, s.serving_description, s.carbs_g, s.calories_kcal
		  FROM food_servings s JOIN foods f ON f.id = s.food_id
		 WHERE s.id = ANY($2) AND s.deleted_at IS NULL AND f.deleted_at IS NULL AND `+foodVisible, userID, ids)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]domain.MealItem{}
	for rows.Next() {
		var it domain.MealItem
		if err := rows.Scan(&it.ServingID, &it.FoodID, &it.FoodName, &it.ServingDescription, &it.CarbsG, &it.CaloriesKcal); err != nil {
			rows.Close()
			return nil, err
		}
		out[it.ServingID] = it
	}
	return out, rows.Err()
}
