package openfoodfacts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/voltik/voltik/backend/internal/domain"
)

func server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "Voltik/test (test@example.com)" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/api/v2/product/5601234567890.json":
			_, _ = w.Write([]byte(`{"status":1,"product":{"product_name":"Natural yoghurt","product_name_pt":"Iogurte natural",
				"brands":"Marca Exemplo, Outra","serving_quantity":"125","product_quantity":120,"product_quantity_unit":"g",
				"nutriments":{"carbohydrates_100g":5.2,"sugars_100g":"5.6","energy-kcal_100g":61,"proteins_100g":3.9,
				"fat_100g":3.1,"saturated-fat_100g":2,"sodium_100g":0.04,"fiber_100g":null}}}`))
		case "/api/v2/product/5600000000000.json":
			_, _ = w.Write([]byte(`{"status":1,"product":{"product_name":"Sem dados","nutriments":{}}}`))
		case "/api/v2/product/5609999999999.json":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":0,"status_verbose":"product not found"}`))
		}
	}))
}

func TestProduct(t *testing.T) {
	srv := server(t)
	defer srv.Close()
	c := New("Voltik/test (test@example.com)").WithBaseURL(srv.URL)

	f, found, err := c.Product(context.Background(), "5601234567890")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if f.Name != "Iogurte natural" || *f.Brand != "Marca Exemplo" || f.Type != domain.FoodBrand || f.DataSource != domain.SourceOpenFoodFacts || *f.BarcodeEAN != "5601234567890" {
		t.Fatalf("food = %+v", f)
	}
	if len(f.Servings) != 3 {
		t.Fatalf("servings = %+v", f.Servings)
	}
	per100 := f.Servings[0]
	if per100.Description != "100 g" || per100.CarbsG != 5.2 || *per100.SugarsG != 5.2 || per100.CaloriesKcal != 61 || *per100.SodiumMg != 40 || per100.FiberG != nil {
		t.Fatalf("100 g = %+v (sugars are capped at the carbohydrates; sodium in mg)", per100)
	}
	if f.Servings[1].Description != "1 porção (125 g)" || f.Servings[1].CarbsG != 6.5 || f.Servings[2].Description != "1 embalagem (120 g)" {
		t.Fatalf("other servings = %+v", f.Servings[1:])
	}

	for _, code := range []string{"5600000000000", "1234567890123"} {
		if _, found, err := c.Product(context.Background(), code); found || err != nil {
			t.Fatalf("%s: found=%v err=%v, want not found", code, found, err)
		}
	}
	if _, _, err := c.Product(context.Background(), "5609999999999"); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("a server error is service_unavailable: %v", err)
	}
}
