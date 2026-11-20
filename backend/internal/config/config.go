// Package config reads the API configuration from environment variables.
// Real values come from each environment's .env file on the server, never from Git.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Shortest JWT_SECRET accepted: HS256 needs at least 256 bits.
const minJWTSecretLength = 32

type Config struct {
	Environment string          // local | staging | production
	Port        string          // HTTP port inside the container
	DatabaseURL string          // connection URL, using the voltik_api role
	CORSOrigins map[string]bool // frontend origins allowed to call the API
	JWTSecret   []byte          // signs the Voltik session tokens

	// Sign-in providers. A provider without configuration answers 503.
	GoogleClientIDs   []string // web and Android OAuth client IDs
	MicrosoftClientID string   // Microsoft Entra app registration
	AppleClientIDs    []string // Services ID (web) and bundle ID
	FacebookAppID     string
	FacebookAppSecret string
}

// Load reads the environment and reports every missing or invalid variable at once.
func Load() (Config, error) {
	c := Config{
		Environment:       getOr("ENVIRONMENT", "local"),
		Port:              getOr("PORT", "8080"),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		CORSOrigins:       map[string]bool{},
		JWTSecret:         []byte(os.Getenv("JWT_SECRET")),
		GoogleClientIDs:   list("GOOGLE_CLIENT_IDS"),
		MicrosoftClientID: strings.TrimSpace(os.Getenv("MICROSOFT_CLIENT_ID")),
		AppleClientIDs:    list("APPLE_CLIENT_IDS"),
		FacebookAppID:     strings.TrimSpace(os.Getenv("FACEBOOK_APP_ID")),
		FacebookAppSecret: strings.TrimSpace(os.Getenv("FACEBOOK_APP_SECRET")),
	}
	for _, o := range list("CORS_ORIGINS") {
		c.CORSOrigins[o] = true
	}

	var errs []error
	switch c.Environment {
	case "local", "staging", "production":
	default:
		errs = append(errs, fmt.Errorf("ENVIRONMENT must be local, staging or production, not %q", c.Environment))
	}
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if len(c.JWTSecret) < minJWTSecretLength {
		errs = append(errs, fmt.Errorf("JWT_SECRET must have at least %d characters (openssl rand -hex 32)", minJWTSecretLength))
	}
	if (c.FacebookAppID == "") != (c.FacebookAppSecret == "") {
		errs = append(errs, errors.New("FACEBOOK_APP_ID and FACEBOOK_APP_SECRET go together"))
	}
	return c, errors.Join(errs...)
}

func getOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// list reads a comma-separated variable, ignoring blanks.
func list(key string) []string {
	var out []string
	for _, v := range strings.Split(os.Getenv(key), ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
