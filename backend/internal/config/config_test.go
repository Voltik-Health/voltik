package config

import "testing"

func TestLoadCORSOrigins(t *testing.T) {
	t.Setenv("CORS_ORIGINS", "http://localhost:5173, https://app.example.com")
	c := Load()
	if !c.CORSOrigins["http://localhost:5173"] || !c.CORSOrigins["https://app.example.com"] {
		t.Fatalf("origins not loaded: %v", c.CORSOrigins)
	}
	if c.Environment != "local" || c.Port != "8080" {
		t.Fatalf("wrong defaults: %+v", c)
	}
}
