package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/voltik/voltik/backend/internal/domain"
)

// fakeProvider publishes the keys of a fake OpenID Connect provider and signs
// ID tokens with them, like Google, Microsoft or Apple would.
type fakeProvider struct {
	key  *rsa.PrivateKey
	jwks *httptest.Server
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.RawURLEncoding.EncodeToString
	body, _ := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "kid": "test-key", "use": "sig", "alg": "RS256",
		"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
	}}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return &fakeProvider{key: key, jwks: srv}
}

func (p *fakeProvider) sign(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "test-key"
	s, err := tok.SignedString(p.key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (p *fakeProvider) config(issuer string, clientIDs ...string) OIDCConfig {
	return OIDCConfig{Issuer: issuer, JWKSURL: p.jwks.URL, ClientIDs: clientIDs}
}

// claimsFor returns valid claims: tests change what they need.
func claimsFor(issuer, audience string) jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"iss": issuer, "aud": audience, "sub": "subject-1",
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
}

func with(c jwt.MapClaims, changes map[string]any) jwt.MapClaims {
	out := jwt.MapClaims{}
	for k, v := range c {
		out[k] = v
	}
	for k, v := range changes {
		if v == nil {
			delete(out, k)
		} else {
			out[k] = v
		}
	}
	return out
}

const googleIssuer = "https://accounts.google.com"

func TestGoogleVerifier(t *testing.T) {
	p := newFakeProvider(t)
	v := NewGoogleVerifier(context.Background(), p.config(googleIssuer, "web-client", "android-client"))
	base := with(claimsFor(googleIssuer, "android-client"), map[string]any{
		"email": "manuel@example.com", "email_verified": true, "name": "Manuel Sousa",
		"picture": "https://lh3.googleusercontent.com/a/photo",
	})

	id, err := v.Verify(context.Background(), p.sign(t, base))
	if err != nil {
		t.Fatal(err)
	}
	if id.Provider != "google" || id.Subject != "subject-1" || id.FullName != "Manuel Sousa" ||
		id.Email == nil || *id.Email != "manuel@example.com" || id.PhotoURL == nil {
		t.Fatalf("identity = %+v", id)
	}

	id, err = v.Verify(context.Background(), p.sign(t, with(base, map[string]any{"email_verified": false})))
	if err != nil || id.Email != nil {
		t.Fatalf("unverified email kept: %+v, %v", id, err)
	}

	id, err = v.Verify(context.Background(), p.sign(t, with(base, map[string]any{"iss": "accounts.google.com"})))
	if err != nil || id.Subject != "subject-1" {
		t.Fatalf("issuer without scheme rejected: %v", err)
	}

	other := newFakeProvider(t)
	rejected := map[string]string{
		"other audience": p.sign(t, with(base, map[string]any{"aud": "someone-else"})),
		"expired":        p.sign(t, with(base, map[string]any{"exp": time.Now().Add(-time.Minute).Unix()})),
		"other issuer":   p.sign(t, with(base, map[string]any{"iss": "https://evil.example.com"})),
		"other key":      other.sign(t, base),
		"not a jwt":      "abc",
	}
	for name, token := range rejected {
		t.Run(name, func(t *testing.T) {
			if _, err := v.Verify(context.Background(), token); !errors.Is(err, domain.ErrInvalidProviderToken) {
				t.Fatalf("err = %v, want ErrInvalidProviderToken", err)
			}
		})
	}
}

func TestMicrosoftVerifier(t *testing.T) {
	p := newFakeProvider(t)
	cfg := p.config("https://login.microsoftonline.com/{tenantid}/v2.0", "voltik-app")
	v := NewMicrosoftVerifier(context.Background(), cfg)
	issuer := func(tenant string) string { return "https://login.microsoftonline.com/" + tenant + "/v2.0" }
	const work = "11111111-2222-3333-4444-555555555555"

	tests := []struct {
		name      string
		claims    jwt.MapClaims
		wantEmail bool
	}{
		{"personal account", with(claimsFor(issuer(microsoftConsumerTenant), "voltik-app"),
			map[string]any{"tid": microsoftConsumerTenant, "email": "ana@outlook.com"}), true},
		{"work account, domain not owned", with(claimsFor(issuer(work), "voltik-app"),
			map[string]any{"tid": work, "email": "ceo@bank.example"}), false},
		{"work account, domain owned", with(claimsFor(issuer(work), "voltik-app"),
			map[string]any{"tid": work, "email": "ana@clinic.example", "xms_edov": true}), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := v.Verify(context.Background(), p.sign(t, tt.claims))
			if err != nil {
				t.Fatal(err)
			}
			if (id.Email != nil) != tt.wantEmail {
				t.Fatalf("email = %v, want kept: %v", id.Email, tt.wantEmail)
			}
		})
	}

	// The issuer must name the tenant of the token
	spoofed := with(claimsFor(issuer(work), "voltik-app"), map[string]any{"tid": microsoftConsumerTenant, "email": "x@y.z"})
	if _, err := v.Verify(context.Background(), p.sign(t, spoofed)); !errors.Is(err, domain.ErrInvalidProviderToken) {
		t.Fatalf("issuer of another tenant accepted: %v", err)
	}
	if _, err := v.Verify(context.Background(), p.sign(t, with(spoofed, map[string]any{"tid": nil}))); !errors.Is(err, domain.ErrInvalidProviderToken) {
		t.Fatalf("token without tenant accepted: %v", err)
	}
}

func TestAppleVerifier(t *testing.T) {
	p := newFakeProvider(t)
	const issuer = "https://appleid.apple.com"
	v := NewAppleVerifier(context.Background(), p.config(issuer, "pt.voltik.web"))
	base := with(claimsFor(issuer, "pt.voltik.web"), map[string]any{"email": "abc@privaterelay.appleid.com"})

	id, err := v.Verify(context.Background(), p.sign(t, with(base, map[string]any{"email_verified": "true"})))
	if err != nil || id.Email == nil || id.FullName != "" {
		t.Fatalf("identity = %+v, %v", id, err)
	}
	id, err = v.Verify(context.Background(), p.sign(t, with(base, map[string]any{"email_verified": "false"})))
	if err != nil || id.Email != nil {
		t.Fatalf("unverified email kept: %+v, %v", id, err)
	}
}
