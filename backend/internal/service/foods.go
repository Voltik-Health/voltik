package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

// FoodStore is what the food catalogue needs from the database.
type FoodStore interface {
	SearchFoods(ctx context.Context, userID uuid.UUID, q string, limit int) ([]domain.Food, error)
	GetFood(ctx context.Context, userID, foodID uuid.UUID) (domain.Food, error)
	FoodByBarcode(ctx context.Context, userID uuid.UUID, ean string) (domain.Food, error)
	CreateFood(ctx context.Context, f domain.Food) (domain.Food, error)
	ListFoodCategories(ctx context.Context) ([]domain.FoodCategory, error)
	CreateFoodCategory(ctx context.Context, name string) (domain.FoodCategory, error)
	RenameFoodCategory(ctx context.Context, id uuid.UUID, name string) (domain.FoodCategory, error)
	DeleteFoodCategory(ctx context.Context, id uuid.UUID) error
	UpdateFood(ctx context.Context, id uuid.UUID, apply func(*domain.Food) error) (domain.Food, error)
	DeleteFood(ctx context.Context, id uuid.UUID) error
	VerifyFood(ctx context.Context, id uuid.UUID) (domain.Food, error)
	AddFoodComponent(ctx context.Context, dishID, componentID uuid.UUID, grams float64) (domain.Food, error)
	RemoveFoodComponent(ctx context.Context, dishID, componentRowID uuid.UUID) (domain.Food, error)
	AddFoodServing(ctx context.Context, foodID uuid.UUID, sv domain.FoodServing) (domain.FoodServing, error)
	UpdateFoodServing(ctx context.Context, foodID, servingID uuid.UUID, apply func(*domain.FoodServing) error) (domain.FoodServing, error)
	DeleteFoodServing(ctx context.Context, foodID, servingID uuid.UUID) error
	FoodsForReview(ctx context.Context, verified *bool, q *string, limit int, cursor string) ([]domain.Food, string, error)
	IsNutritionReviewer(ctx context.Context, userID uuid.UUID) (bool, error)
}

// ProductLookup finds packaged products by barcode (Open Food Facts).
type ProductLookup interface {
	Product(ctx context.Context, barcode string) (domain.Food, bool, error)
}

// Foods runs the food catalogue: search, barcodes and custom foods for the
// patients (UC093–UC095) and its management for the reviewers (UC096–UC099).
type Foods struct {
	store    FoodStore
	products ProductLookup
	log      *slog.Logger
}

func NewFoods(store FoodStore, products ProductLookup, log *slog.Logger) *Foods {
	return &Foods{store: store, products: products, log: log}
}

// Search finds foods by name (UC093).
func (s *Foods) Search(ctx context.Context, a domain.Account, q string, limit int) ([]domain.Food, error) {
	q = strings.TrimSpace(q)
	if n := utf8.RuneCountInString(q); n < 2 || n > 100 {
		return nil, domain.Invalid("q", "must have between 2 and 100 characters")
	}
	return s.store.SearchFoods(ctx, a.ID, q, limit)
}

func (s *Foods) Get(ctx context.Context, a domain.Account, id uuid.UUID) (domain.Food, error) {
	return s.store.GetFood(ctx, a.ID, id)
}

// ByBarcode finds a product by barcode in the catalogue or in Open Food
// Facts, and saves what Open Food Facts knows (UC094).
func (s *Foods) ByBarcode(ctx context.Context, a domain.Account, ean string) (domain.Food, error) {
	if !domain.ValidBarcode(ean) {
		return domain.Food{}, domain.Invalid("ean", "must have 8 to 14 digits")
	}
	f, err := s.store.FoodByBarcode(ctx, a.ID, ean)
	if !errors.Is(err, domain.ErrNotFound) {
		return f, err
	}
	lookup, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	product, found, err := s.products.Product(lookup, ean)
	if err != nil {
		s.log.Warn("open food facts lookup failed", "error", err)
		return domain.Food{}, domain.ErrServiceUnavailable
	}
	if !found {
		return domain.Food{}, domain.ErrNotFound
	}
	saved, err := s.store.CreateFood(ctx, product)
	if errors.Is(err, domain.ErrBarcodeInUse) { // saved by another request meanwhile
		return s.store.FoodByBarcode(ctx, a.ID, ean)
	}
	return saved, err
}

// CreateCustom adds a food the patient could not find (UC095). Only the
// author sees it until it is verified.
func (s *Foods) CreateCustom(ctx context.Context, a domain.Account, f domain.Food) (domain.Food, error) {
	if _, err := patientOf(a); err != nil {
		return f, err
	}
	trimFood(&f)
	if f.Type == "" {
		f.Type = domain.FoodGeneric
	}
	if f.Type != domain.FoodGeneric && f.Type != domain.FoodBrand {
		return f, domain.Invalid("food_type", "must be generic or brand")
	}
	if err := domain.ValidateFood(f, true); err != nil {
		return f, err
	}
	f.DataSource, f.IsVerified, f.CreatedBy = domain.SourceUserCustom, false, &a.ID
	return s.store.CreateFood(ctx, f)
}

func trimFood(f *domain.Food) {
	f.Name = strings.TrimSpace(f.Name)
	if f.Brand != nil {
		b := strings.TrimSpace(*f.Brand)
		f.Brand = &b
	}
	for i := range f.Servings {
		f.Servings[i].Description = strings.TrimSpace(f.Servings[i].Description)
	}
}

func (s *Foods) Categories(ctx context.Context) ([]domain.FoodCategory, error) {
	return s.store.ListFoodCategories(ctx)
}

// --- Catalogue management (UC096–UC099) -------------------------------------------

func requireAdmin(a domain.Account) error {
	if !a.HasRole(domain.RoleAdmin) {
		return domain.ErrForbidden
	}
	return nil
}

// requireReviewer lets platform admins and nutrition professionals through (UC099).
func (s *Foods) requireReviewer(ctx context.Context, a domain.Account) error {
	if a.HasRole(domain.RoleAdmin) {
		return nil
	}
	if a.HasRole(domain.RoleHealthProfessional) {
		ok, err := s.store.IsNutritionReviewer(ctx, a.ID)
		if err != nil || ok {
			return err
		}
	}
	return domain.ErrForbidden
}

func (s *Foods) CreateCategory(ctx context.Context, a domain.Account, name string) (domain.FoodCategory, error) {
	if err := requireAdmin(a); err != nil {
		return domain.FoodCategory{}, err
	}
	name = strings.TrimSpace(name)
	if err := domain.ValidateCategoryName(name); err != nil {
		return domain.FoodCategory{}, err
	}
	return s.store.CreateFoodCategory(ctx, name)
}

func (s *Foods) RenameCategory(ctx context.Context, a domain.Account, id uuid.UUID, name string) (domain.FoodCategory, error) {
	if err := requireAdmin(a); err != nil {
		return domain.FoodCategory{}, err
	}
	name = strings.TrimSpace(name)
	if err := domain.ValidateCategoryName(name); err != nil {
		return domain.FoodCategory{}, err
	}
	return s.store.RenameFoodCategory(ctx, id, name)
}

func (s *Foods) DeleteCategory(ctx context.Context, a domain.Account, id uuid.UUID) error {
	if err := requireAdmin(a); err != nil {
		return err
	}
	return s.store.DeleteFoodCategory(ctx, id)
}

// AdminCreate adds a verified food of the Voltik team (UC097). A composite
// dish gets its servings from its ingredients.
func (s *Foods) AdminCreate(ctx context.Context, a domain.Account, f domain.Food) (domain.Food, error) {
	if err := requireAdmin(a); err != nil {
		return f, err
	}
	trimFood(&f)
	switch f.Type {
	case domain.FoodGeneric, domain.FoodBrand, domain.FoodCompositeDish:
	default:
		return f, domain.Invalid("food_type", "must be one of the listed values")
	}
	if err := domain.ValidateFood(f, f.Type != domain.FoodCompositeDish); err != nil {
		return f, err
	}
	if f.Type == domain.FoodCompositeDish && len(f.Servings) > 0 {
		return f, domain.Invalid("servings", "a composite dish gets its servings from its ingredients")
	}
	f.DataSource, f.IsVerified, f.CreatedBy = domain.SourcePortugueseTraditional, true, &a.ID
	return s.store.CreateFood(ctx, f)
}

// FoodChange is a correction of a food by a reviewer.
type FoodChange struct {
	Name                              *string
	SetBrand, SetBarcode, SetCategory bool
	Brand, Barcode                    *string
	CategoryID                        *uuid.UUID
}

func (s *Foods) AdminUpdate(ctx context.Context, a domain.Account, id uuid.UUID, c FoodChange) (domain.Food, error) {
	if err := s.requireReviewer(ctx, a); err != nil {
		return domain.Food{}, err
	}
	return s.store.UpdateFood(ctx, id, func(f *domain.Food) error {
		if c.Name != nil {
			f.Name = *c.Name
		}
		if c.SetBrand {
			f.Brand = c.Brand
		}
		if c.SetBarcode {
			f.BarcodeEAN = c.Barcode
		}
		if c.SetCategory {
			f.Category = nil
			if c.CategoryID != nil {
				f.Category = &domain.FoodCategoryRef{ID: *c.CategoryID}
			}
		}
		keep := f.Servings
		f.Servings = nil // the servings are not part of this change
		trimFood(f)
		err := domain.ValidateFood(*f, false)
		f.Servings = keep
		return err
	})
}

func (s *Foods) AdminDelete(ctx context.Context, a domain.Account, id uuid.UUID) error {
	if err := s.requireReviewer(ctx, a); err != nil {
		return err
	}
	return s.store.DeleteFood(ctx, id)
}

func (s *Foods) Verify(ctx context.Context, a domain.Account, id uuid.UUID) (domain.Food, error) {
	if err := s.requireReviewer(ctx, a); err != nil {
		return domain.Food{}, err
	}
	return s.store.VerifyFood(ctx, id)
}

func (s *Foods) AddComponent(ctx context.Context, a domain.Account, dishID, componentID uuid.UUID, grams float64) (domain.Food, error) {
	if err := requireAdmin(a); err != nil {
		return domain.Food{}, err
	}
	if grams < 0.01 || grams > 10000 {
		return domain.Food{}, domain.Invalid("default_quantity_g", "must be between 0.01 and 10000")
	}
	return s.store.AddFoodComponent(ctx, dishID, componentID, grams)
}

func (s *Foods) RemoveComponent(ctx context.Context, a domain.Account, dishID, componentRowID uuid.UUID) (domain.Food, error) {
	if err := requireAdmin(a); err != nil {
		return domain.Food{}, err
	}
	return s.store.RemoveFoodComponent(ctx, dishID, componentRowID)
}

func (s *Foods) AddServing(ctx context.Context, a domain.Account, foodID uuid.UUID, sv domain.FoodServing) (domain.FoodServing, error) {
	if err := s.requireReviewer(ctx, a); err != nil {
		return sv, err
	}
	sv.Description = strings.TrimSpace(sv.Description)
	if err := domain.ValidateServing(sv); err != nil {
		return sv, err
	}
	return s.store.AddFoodServing(ctx, foodID, sv)
}

func (s *Foods) UpdateServing(ctx context.Context, a domain.Account, foodID, servingID uuid.UUID, change func(*domain.FoodServing)) (domain.FoodServing, error) {
	if err := s.requireReviewer(ctx, a); err != nil {
		return domain.FoodServing{}, err
	}
	return s.store.UpdateFoodServing(ctx, foodID, servingID, func(sv *domain.FoodServing) error {
		change(sv)
		sv.Description = strings.TrimSpace(sv.Description)
		return domain.ValidateServing(*sv)
	})
}

func (s *Foods) DeleteServing(ctx context.Context, a domain.Account, foodID, servingID uuid.UUID) error {
	if err := s.requireReviewer(ctx, a); err != nil {
		return err
	}
	return s.store.DeleteFoodServing(ctx, foodID, servingID)
}

func (s *Foods) ForReview(ctx context.Context, a domain.Account, verified *bool, q *string, limit int, cursor string) ([]domain.Food, string, error) {
	if err := s.requireReviewer(ctx, a); err != nil {
		return nil, "", err
	}
	if q != nil {
		trimmed := strings.TrimSpace(*q)
		if n := utf8.RuneCountInString(trimmed); n < 2 || n > 100 {
			return nil, "", domain.Invalid("q", "must have between 2 and 100 characters")
		}
		q = &trimmed
	}
	return s.store.FoodsForReview(ctx, verified, q, limit, cursor)
}
