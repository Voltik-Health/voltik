package config

import "testing"

func TestCarregarOrigensCORS(t *testing.T) {
	t.Setenv("CORS_ORIGENS", "http://localhost:5173, https://app.exemplo.pt")
	c := Carregar()
	if !c.OrigensCORS["http://localhost:5173"] || !c.OrigensCORS["https://app.exemplo.pt"] {
		t.Fatalf("origens não carregadas: %v", c.OrigensCORS)
	}
	if c.Ambiente != "local" || c.Porta != "8080" {
		t.Fatalf("valores por omissão errados: %+v", c)
	}
}
