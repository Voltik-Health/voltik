// Package config reads the API configuration from environment variables.
// Real values come from each environment's .env file on the server, never from Git.
package config

import (
	"encoding/base64"
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

	// Health data (DVT-79). Every integration is optional: without it the
	// API still works and says in the log what is off.
	PublicURL              string // public base URL, for signed file links and the Nightscout URL
	FirebaseCredentials    []byte // service-account JSON of the Firebase project (push)
	GeminiAPIKey           string // meal photos and report summaries
	GeminiModel            string
	OpenFoodFactsUserAgent string // Open Food Facts asks for an app name and a contact email
	SMSProvider            string // log | twilio
	TwilioAccountSID       string
	TwilioAuthToken        string
	TwilioFrom             string
	SimulatorEnabled       bool // CGM simulator for the demo sensors; never in production
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
	c.PublicURL = strings.TrimRight(getOr("PUBLIC_URL", "http://localhost:"+c.Port), "/")
	c.GeminiAPIKey = strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	c.GeminiModel = strings.TrimSpace(os.Getenv("GEMINI_MODEL"))
	c.OpenFoodFactsUserAgent = getOr("OPENFOODFACTS_USER_AGENT", "Voltik/1.0 (voltik.startup@gmail.com)")
	c.SMSProvider = getOr("SMS_PROVIDER", "log")
	c.TwilioAccountSID = strings.TrimSpace(os.Getenv("TWILIO_ACCOUNT_SID"))
	c.TwilioAuthToken = strings.TrimSpace(os.Getenv("TWILIO_AUTH_TOKEN"))
	c.TwilioFrom = strings.TrimSpace(os.Getenv("TWILIO_FROM"))
	c.SimulatorEnabled = os.Getenv("SIMULATOR_ENABLED") == "true"

	var errs []error
	creds, err := firebaseCredentials()
	if err != nil {
		errs = append(errs, err)
	}
	c.FirebaseCredentials = creds
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
	switch c.SMSProvider {
	case "log":
	case "twilio":
		if c.TwilioAccountSID == "" || c.TwilioAuthToken == "" || c.TwilioFrom == "" {
			errs = append(errs, errors.New("SMS_PROVIDER=twilio needs TWILIO_ACCOUNT_SID, TWILIO_AUTH_TOKEN and TWILIO_FROM"))
		}
	default:
		errs = append(errs, fmt.Errorf("SMS_PROVIDER must be log or twilio, not %q", c.SMSProvider))
	}
	if c.SimulatorEnabled && c.Environment == "production" {
		errs = append(errs, errors.New("SIMULATOR_ENABLED cannot be true in production: the simulator writes fictitious readings"))
	}
	if !strings.HasPrefix(c.PublicURL, "http://") && !strings.HasPrefix(c.PublicURL, "https://") {
		errs = append(errs, fmt.Errorf("PUBLIC_URL must start with http:// or https://, not %q", c.PublicURL))
	}
	return c, errors.Join(errs...)
}

// firebaseCredentials reads the service-account JSON from the file named by
// FIREBASE_CREDENTIALS_FILE, or from FIREBASE_CREDENTIALS_BASE64.
func firebaseCredentials() ([]byte, error) {
	if path := strings.TrimSpace(os.Getenv("FIREBASE_CREDENTIALS_FILE")); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("FIREBASE_CREDENTIALS_FILE: %w", err)
		}
		return b, nil
	}
	if enc := strings.TrimSpace(os.Getenv("FIREBASE_CREDENTIALS_BASE64")); enc != "" {
		b, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return nil, fmt.Errorf("FIREBASE_CREDENTIALS_BASE64 is not valid base64: %w", err)
		}
		return b, nil
	}
	return nil, nil
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
