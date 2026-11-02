package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/voltik/voltik/backend/internal/domain"
)

// fakeGraph answers debug_token and /me like the Graph API.
type fakeGraph struct {
	debugStatus int
	debug       map[string]any
	me          map[string]any
	gotProof    string
}

func (g *fakeGraph) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/debug_token":
			if r.URL.Query().Get("access_token") != "app-id|app-secret" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if g.debugStatus != 0 {
				w.WriteHeader(g.debugStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": g.debug})
		case "/me":
			g.gotProof = r.URL.Query().Get("appsecret_proof")
			_ = json.NewEncoder(w).Encode(g.me)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func validGraph() *fakeGraph {
	return &fakeGraph{
		debug: map[string]any{"app_id": "app-id", "is_valid": true, "user_id": "fb-42"},
		me:    map[string]any{"id": "fb-42", "name": "Inês Sousa", "email": "ines@example.com"},
	}
}

func TestFacebookVerifier(t *testing.T) {
	g := validGraph()
	v := NewFacebookVerifier("app-id", "app-secret", g.server(t).URL)

	id, err := v.Verify(context.Background(), "user-token")
	if err != nil {
		t.Fatal(err)
	}
	if id.Provider != "facebook" || id.Subject != "fb-42" || id.FullName != "Inês Sousa" || id.Email == nil {
		t.Fatalf("identity = %+v", id)
	}
	mac := hmac.New(sha256.New, []byte("app-secret"))
	mac.Write([]byte("user-token"))
	if g.gotProof != hex.EncodeToString(mac.Sum(nil)) {
		t.Fatalf("appsecret_proof = %q", g.gotProof)
	}

	delete(g.me, "email")
	id, err = v.Verify(context.Background(), "user-token")
	if err != nil || id.Email != nil {
		t.Fatalf("without the email permission: %+v, %v", id, err)
	}
}

func TestFacebookVerifierRejects(t *testing.T) {
	tests := []struct {
		name   string
		change func(*fakeGraph)
		want   error
	}{
		{"invalid token", func(g *fakeGraph) { g.debug["is_valid"] = false }, domain.ErrInvalidProviderToken},
		{"token of another app", func(g *fakeGraph) { g.debug["app_id"] = "other-app" }, domain.ErrInvalidProviderToken},
		{"me answers another user", func(g *fakeGraph) { g.me["id"] = "fb-99" }, domain.ErrInvalidProviderToken},
		{"graph api rejects", func(g *fakeGraph) { g.debugStatus = http.StatusBadRequest }, domain.ErrInvalidProviderToken},
		{"graph api down", func(g *fakeGraph) { g.debugStatus = http.StatusBadGateway }, domain.ErrServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := validGraph()
			tt.change(g)
			v := NewFacebookVerifier("app-id", "app-secret", g.server(t).URL)
			if _, err := v.Verify(context.Background(), "user-token"); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestFacebookErrorsHideTheToken(t *testing.T) {
	v := NewFacebookVerifier("app-id", "app-secret", "http://127.0.0.1:1") // nothing listens there
	_, err := v.Verify(context.Background(), "secret-user-token")
	if !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("err = %v, want ErrServiceUnavailable", err)
	}
	if strings.Contains(err.Error(), "secret-user-token") || strings.Contains(err.Error(), "app-secret") {
		t.Fatalf("error leaks a secret: %v", err)
	}
}
