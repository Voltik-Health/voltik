// Entry point of the Voltik API: reads the configuration, opens the database
// pool, wires the layers (handler → service → repository) and serves HTTP.
// It also runs the background jobs: the daily retention job
// (internal/retention), the push and SMS outboxes, the alert escalation, the
// sensor monitor, the photo analysis, the post-meal impact, the weekly
// reports and, in staging, the CGM simulator.
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
	"github.com/voltik/voltik/backend/internal/gemini"
	"github.com/voltik/voltik/backend/internal/handler"
	"github.com/voltik/voltik/backend/internal/jobs"
	"github.com/voltik/voltik/backend/internal/openfoodfacts"
	"github.com/voltik/voltik/backend/internal/push"
	"github.com/voltik/voltik/backend/internal/repository"
	"github.com/voltik/voltik/backend/internal/retention"
	"github.com/voltik/voltik/backend/internal/service"
	"github.com/voltik/voltik/backend/internal/simulator"
	"github.com/voltik/voltik/backend/internal/sms"
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

	// Health data (DVT-79)
	var sender push.Sender = push.Disabled{}
	if len(cfg.FirebaseCredentials) > 0 {
		fcm, err := push.NewFCM(ctx, cfg.FirebaseCredentials)
		if err != nil {
			log.Error("invalid Firebase credentials", "error", err)
			os.Exit(1)
		}
		sender = fcm
	} else {
		log.Warn("push notifications are off: set FIREBASE_CREDENTIALS_FILE or FIREBASE_CREDENTIALS_BASE64")
	}
	outbox := push.NewOutbox(store, sender, log)
	ai := gemini.New(cfg.GeminiAPIKey, cfg.GeminiModel)
	if !ai.Configured() {
		log.Warn("meal photo analysis and report summaries are off: set GEMINI_API_KEY")
	}
	var smsSender sms.Sender = sms.Log{Logger: log}
	twilioToken := ""
	if cfg.SMSProvider == "twilio" {
		smsSender = sms.Twilio{AccountSID: cfg.TwilioAccountSID, AuthToken: cfg.TwilioAuthToken, From: cfg.TwilioFrom,
			StatusCallback: cfg.PublicURL + "/v1/webhooks/sms"}
		twilioToken = cfg.TwilioAuthToken
	}
	log.Info("escalation texts", "sms_provider", smsSender.Name())
	files := service.NewFiles(cfg.JWTSecret, cfg.PublicURL, store)
	health := service.NewHealth(store, outbox, service.NoPredictions{}, log)
	alerts := service.NewAlerts(store, outbox, outbox, log)
	foods := service.NewFoods(store, openfoodfacts.New(cfg.OpenFoodFactsUserAgent), log)
	diary := service.NewDiary(store, ai, outbox, log)
	photos := jobs.NewTrigger()
	diary.SetAnalysisWaker(photos)
	history := service.NewHistory(store, ai, outbox, files, log)

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

		PublicURL:       cfg.PublicURL,
		HealthData:      health,
		Alerts:          alerts,
		Foods:           foods,
		Diary:           diary,
		History:         history,
		Files:           files,
		SMSReceipts:     store,
		TwilioAuthToken: twilioToken,
	})

	go retention.NewJob(store, log).Schedule(ctx)
	go outbox.Run(ctx)
	smsOutbox := sms.NewOutbox(store, smsSender, log)
	go jobs.Every(ctx, log, "escalation", 30*time.Second, func(ctx context.Context) {
		alerts.Escalate(ctx)
		smsOutbox.Drain(ctx)
	})
	go jobs.Every(ctx, log, "sensor-monitor", 5*time.Minute, alerts.CheckSensors)
	go jobs.EveryOrWake(ctx, log, "photo-analysis", 15*time.Second, photos, diary.AnalyzePhotos)
	go jobs.Every(ctx, log, "post-meal-impact", 5*time.Minute, diary.ComputeImpacts)
	go jobs.Every(ctx, log, "weekly-reports", 10*time.Minute, history.WeeklyReports)
	if cfg.SimulatorEnabled {
		traces, err := simulator.LoadTraces()
		if err != nil {
			log.Error("simulator: could not load the traces", "error", err)
			os.Exit(1)
		}
		sim := simulator.New(store, health, traces, log)
		go jobs.Every(ctx, log, "simulator", time.Minute, sim.Tick)
		log.Info("CGM simulator on", "traces", len(traces))
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second, // a report with the Iris summary and its PDF can take a while
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
