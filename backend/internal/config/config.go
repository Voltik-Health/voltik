// Package config reads the API configuration from environment variables.
// Real values come from each environment's .env file on the server, never from Git.
package config

import (
	"os"
	"strings"
)

type Config struct {
	Environment string          // local | staging | production
	Port        string          // HTTP port inside the container
	DatabaseURL string          // connection URL, using the voltik_api role
	CORSOrigins map[string]bool // frontend origins allowed to call the API
}

func Load() Config {
	origins := map[string]bool{}
	for _, o := range strings.Split(os.Getenv("CORS_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins[o] = true
		}
	}
	return Config{
		Environment: getOr("ENVIRONMENT", "local"),
		Port:        getOr("PORT", "8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		CORSOrigins: origins,
	}
}

func getOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
