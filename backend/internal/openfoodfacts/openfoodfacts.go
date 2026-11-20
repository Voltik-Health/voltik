// Package openfoodfacts looks packaged products up by barcode in Open Food
// Facts (https://openfoodfacts.org), an open database (ODbL) of products
// sold in Portugal and elsewhere (SRS UC094, in place of FatSecret).
//
// Open Food Facts asks every client to identify itself with a User-Agent
// that has a contact email, and limits reads per minute, so the API saves
// each product it finds in the Voltik catalogue and asks only once.
package openfoodfacts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/voltik/voltik/backend/internal/domain"
)

// ErrUnavailable means Open Food Facts could not be reached.
var ErrUnavailable = fmt.Errorf("open food facts is not available: %w", domain.ErrServiceUnavailable)

// Client reads products from the Open Food Facts API v2.
type Client struct {
	userAgent string
	baseURL   string
	http      *http.Client
}

func New(userAgent string) *Client {
	return &Client{userAgent: userAgent, baseURL: "https://world.openfoodfacts.org", http: &http.Client{Timeout: 4 * time.Second}}
}

// WithBaseURL points the client at another server (tests).
func (c *Client) WithBaseURL(u string) *Client {
	copy := *c
	copy.baseURL = strings.TrimRight(u, "/")
	return &copy
}

// Product returns the product with this barcode, ready for the catalogue:
// a food of type brand, source open_food_facts, with a "100 g" serving and,
// when Open Food Facts knows them, the serving and the package. found is
// false when nobody knows the code or the product has no carbohydrates and
// energy (the app then offers to create the food).
func (c *Client) Product(ctx context.Context, barcode string) (domain.Food, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v2/product/"+barcode+".json?fields="+
		"product_name,product_name_pt,brands,nutriments,serving_quantity,serving_size,product_quantity,product_quantity_unit", nil)
	if err != nil {
		return domain.Food{}, false, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return domain.Food{}, false, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return domain.Food{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Food{}, false, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}
	var body struct {
		Status  int `json:"status"`
		Product struct {
			Name           string                     `json:"product_name"`
			NamePT         string                     `json:"product_name_pt"`
			Brands         string                     `json:"brands"`
			Nutriments     map[string]json.RawMessage `json:"nutriments"`
			ServingQty     json.RawMessage            `json:"serving_quantity"`
			ServingSize    string                     `json:"serving_size"`
			ProductQty     json.RawMessage            `json:"product_quantity"`
			ProductQtyUnit string                     `json:"product_quantity_unit"`
		} `json:"product"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&body); err != nil {
		return domain.Food{}, false, fmt.Errorf("%w: unreadable answer: %v", ErrUnavailable, err)
	}
	p := body.Product
	if body.Status != 1 {
		return domain.Food{}, false, nil
	}
	name := strings.TrimSpace(p.NamePT)
	if name == "" {
		name = strings.TrimSpace(p.Name)
	}
	carbs, okCarbs := number(p.Nutriments["carbohydrates_100g"])
	kcal, okKcal := number(p.Nutriments["energy-kcal_100g"])
	if !okKcal {
		if kj, ok := number(p.Nutriments["energy-kj_100g"]); ok {
			kcal, okKcal = kj/4.184, true
		}
	}
	if name == "" || !okCarbs || !okKcal {
		return domain.Food{}, false, nil
	}
	per100 := domain.Nutrition{CarbsG: carbs, CaloriesKcal: kcal}
	per100.FiberG = optional(p.Nutriments["fiber_100g"])
	per100.SugarsG = optional(p.Nutriments["sugars_100g"])
	per100.ProteinG, _ = number(p.Nutriments["proteins_100g"])
	per100.FatG, _ = number(p.Nutriments["fat_100g"])
	per100.SaturatedFatG = optional(p.Nutriments["saturated-fat_100g"])
	if na := optional(p.Nutriments["sodium_100g"]); na != nil {
		mg := *na * 1000 // Open Food Facts gives sodium in grams
		per100.SodiumMg = &mg
	}
	// Some products have small inconsistencies; keep the rules of the catalogue.
	if per100.SugarsG != nil && *per100.SugarsG > per100.CarbsG {
		per100.SugarsG = &per100.CarbsG
	}
	if per100.SaturatedFatG != nil && *per100.SaturatedFatG > per100.FatG {
		per100.SaturatedFatG = &per100.FatG
	}

	food := domain.Food{
		Name: truncate(name, 200), Type: domain.FoodBrand, DataSource: domain.SourceOpenFoodFacts,
		BarcodeEAN: &barcode,
	}
	if brand := strings.TrimSpace(strings.Split(p.Brands, ",")[0]); brand != "" {
		b := truncate(brand, 100)
		food.Brand = &b
	}
	add := func(desc string, grams float64) {
		if grams <= 0 || grams > 50000 {
			return
		}
		s := serving(desc, grams, per100)
		if domain.ValidateServing(s) == nil {
			food.Servings = append(food.Servings, s)
		}
	}
	add("100 g", 100)
	if g, ok := number(p.ServingQty); ok && g != 100 {
		add(fmt.Sprintf("1 porção (%s g)", trim(g)), g)
	}
	if g, ok := number(p.ProductQty); ok && strings.EqualFold(p.ProductQtyUnit, "g") && g != 100 {
		add(fmt.Sprintf("1 embalagem (%s g)", trim(g)), g)
	}
	if len(food.Servings) == 0 {
		return domain.Food{}, false, nil
	}
	return food, true, nil
}

func serving(desc string, grams float64, per100 domain.Nutrition) domain.FoodServing {
	f := grams / 100
	scale := func(v *float64) *float64 {
		if v == nil {
			return nil
		}
		x := domain.Round2(*v * f)
		return &x
	}
	unit := "g"
	amount := domain.Round2(grams)
	return domain.FoodServing{
		Description: desc, MetricAmount: &amount, MetricUnit: &unit,
		Nutrition: domain.Nutrition{
			CarbsG: domain.Round2(per100.CarbsG * f), FiberG: scale(per100.FiberG), SugarsG: scale(per100.SugarsG),
			CaloriesKcal: domain.Round2(per100.CaloriesKcal * f), ProteinG: domain.Round2(per100.ProteinG * f),
			FatG: domain.Round2(per100.FatG * f), SaturatedFatG: scale(per100.SaturatedFatG), SodiumMg: scale(per100.SodiumMg),
		},
	}
}

// number reads a value that Open Food Facts sends as a number or as text.
func number(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return f, f >= 0
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if f, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", "."), 64); err == nil && f >= 0 {
			return f, true
		}
	}
	return 0, false
}

func optional(raw json.RawMessage) *float64 {
	if f, ok := number(raw); ok {
		return &f
	}
	return nil
}

func trim(f float64) string { return strconv.FormatFloat(domain.Round2(f), 'f', -1, 64) }

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
