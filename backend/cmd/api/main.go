// Entry point of the Voltik API.
// For now it only exposes /v1/health, to validate the whole chain:
// GitHub -> Jenkins -> Docker image -> staging/production -> HTTPS proxy.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/voltik/voltik/backend/internal/config"
)

// version is set at build time: -ldflags "-X main.version=<commit>"
var version = "dev"

func main() {
	cfg := config.Load()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("environment", cfg.Environment, "version", version)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":      "ok",
			"environment": cfg.Environment,
			"version":     version,
		})
	})

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           withCORS(cfg.CORSOrigins, mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("API starting", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown when Docker sends SIGTERM (e.g. during a new deployment)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Info("API stopped")
}

// withCORS lets the local frontends (e.g. http://localhost:5173) call the staging API.
func withCORS(origins map[string]bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); origins[o] {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
