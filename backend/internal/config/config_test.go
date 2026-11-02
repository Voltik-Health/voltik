package config

import (
	"strings"
	"testing"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func setRequired(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://voltik_api:x@localhost:5432/voltik")
	t.Setenv("JWT_SECRET", testSecret)
}

func TestLoadCORSOrigins(t *testing.T) {
	setRequired(t)
	t.Setenv("CORS_ORIGINS", "http://localhost:5173, https://app.example.com")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.CORSOrigins["http://localhost:5173"] || !c.CORSOrigins["https://app.example.com"] {
		t.Fatalf("origins not loaded: %v", c.CORSOrigins)
	}
	if c.Environment != "local" || c.Port != "8080" {
		t.Fatalf("wrong defaults: %+v", c)
	}
}

func TestLoadProviders(t *testing.T) {
	setRequired(t)
	t.Setenv("GOOGLE_CLIENT_IDS", "web.apps.googleusercontent.com, android.apps.googleusercontent.com,")
	t.Setenv("FACEBOOK_APP_ID", "123")
	t.Setenv("FACEBOOK_APP_SECRET", "s3cret")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.GoogleClientIDs) != 2 || c.GoogleClientIDs[1] != "android.apps.googleusercontent.com" {
		t.Fatalf("google client ids = %q", c.GoogleClientIDs)
	}
	if c.MicrosoftClientID != "" || c.AppleClientIDs != nil {
		t.Fatalf("unset providers should stay empty: %+v", c)
	}
}

func TestLoadReportsEveryProblem(t *testing.T) {
	t.Setenv("ENVIRONMENT", "dev")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("JWT_SECRET", "short")
	t.Setenv("FACEBOOK_APP_ID", "123")
	t.Setenv("FACEBOOK_APP_SECRET", "")
	_, err := Load()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"ENVIRONMENT", "DATABASE_URL", "JWT_SECRET", "FACEBOOK_APP_SECRET"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}
