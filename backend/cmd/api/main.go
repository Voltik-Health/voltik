// Entry point of the Voltik API: reads the configuration, opens the database
// pool, wires the layers (handler → service → repository) and serves HTTP.
// It also runs the daily retention job (internal/retention).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/voltik/voltik/backend/internal/auth"
	"github.com/voltik/voltik/backend/internal/config"
	"github.com/voltik/voltik/backend/internal/database"
	"github.com/voltik/voltik/backend/internal/domain"
	"github.com/voltik/voltik/backend/internal/handler"
	"github.com/voltik/voltik/backend/internal/repository"
	"github.com/voltik/voltik/backend/internal/retention"
	"github.com/voltik/voltik/backend/internal/service"
)

// version is set at build time: -ldflags "-X main.version=<commit>"
var version = "dev"

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		log.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	log = log.With("environment", cfg.Environment, "version", version)

	// Cancelled when Docker sends SIGTERM (e.g. during a new deployment)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("database configuration", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Warn("database not reachable yet", "error", err)
	}
	store := repository.New(pool)

	tokens := auth.NewTokens(cfg.JWTSecret, cfg.Environment)
	verifiers := providers(ctx, cfg)
	for provider := range verifiers {
		log.Info("sign-in provider configured", "provider", provider)
	}

	router := handler.NewRouter(handler.Deps{
		Log:         log,
		Environment: cfg.Environment,
		Version:     version,
		CORSOrigins: cfg.CORSOrigins,
		Tokens:      tokens,
		Accounts:    store,
		Health:      store,
		SignIn:      service.NewSignIn(store, tokens, verifiers),
		Account:     service.NewAccounts(store),
		Patients:    service.NewPatients(store),
		Consents:    service.NewConsents(store),
	})

	go retention.NewJob(store, log).Schedule(ctx)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		log.Info("API starting", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	log.Info("API stopped")
}

// providers returns the sign-in verifiers configured on this server. The
// others answer 503 provider_unavailable, and so does any provider marked
// inactive in identity_providers (Apple, for now).
func providers(ctx context.Context, cfg config.Config) map[string]service.Verifier {
	v := map[string]service.Verifier{}
	if len(cfg.GoogleClientIDs) > 0 {
		v[domain.ProviderGoogle] = auth.NewGoogleVerifier(ctx, auth.GoogleConfig(cfg.GoogleClientIDs))
	}
	if cfg.MicrosoftClientID != "" {
		v[domain.ProviderMicrosoft] = auth.NewMicrosoftVerifier(ctx, auth.MicrosoftConfig(cfg.MicrosoftClientID))
	}
	if len(cfg.AppleClientIDs) > 0 {
		v[domain.ProviderApple] = auth.NewAppleVerifier(ctx, auth.AppleConfig(cfg.AppleClientIDs))
	}
	if cfg.FacebookAppID != "" {
		v[domain.ProviderFacebook] = auth.NewFacebookVerifier(cfg.FacebookAppID, cfg.FacebookAppSecret, auth.FacebookGraphURL)
	}
	return v
}
