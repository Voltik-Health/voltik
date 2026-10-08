// Ponto de entrada da API do Voltik.
// Por agora só expõe /v1/saude, para validar a cadeia completa:
// GitHub -> Jenkins -> imagem Docker -> staging/produção -> proxy HTTPS.
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

// versao é preenchida no build: -ldflags "-X main.versao=<commit>"
var versao = "dev"

func main() {
	cfg := config.Carregar()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("ambiente", cfg.Ambiente, "versao", versao)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/saude", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"estado":   "ok",
			"ambiente": cfg.Ambiente,
			"versao":   versao,
		})
	})

	srv := &http.Server{
		Addr:              ":" + cfg.Porta,
		Handler:           comCORS(cfg.OrigensCORS, mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("API a arrancar", "porta", cfg.Porta)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("erro no servidor", "erro", err)
			os.Exit(1)
		}
	}()

	// Paragem limpa quando o Docker envia SIGTERM (ex.: numa nova publicação)
	parar := make(chan os.Signal, 1)
	signal.Notify(parar, syscall.SIGINT, syscall.SIGTERM)
	<-parar
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Info("API parada")
}

// comCORS deixa os frontends locais (ex.: http://localhost:5173) chamar a API de staging.
func comCORS(origens map[string]bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); origens[o] {
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
