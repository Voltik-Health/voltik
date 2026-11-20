package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef")

func fixedTokens(environment string, now time.Time) *Tokens {
	t := NewTokens(testSecret, environment)
	t.now = func() time.Time { return now }
	return t
}

func TestTokensRoundTrip(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	tokens := fixedTokens("staging", now)
	userID := uuid.New()

	token, expiresAt, err := tokens.Issue(userID)
	if err != nil {
		t.Fatal(err)
	}
	if !expiresAt.Equal(now.Add(7 * 24 * time.Hour)) {
		t.Fatalf("expires at %v", expiresAt)
	}
	got, err := tokens.Parse(token)
	if err != nil || got != userID {
		t.Fatalf("Parse = %v, %v", got, err)
	}
}

func TestTokensRejected(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	userID := uuid.New()
	valid, _, _ := fixedTokens("staging", now).Issue(userID)

	sign := func(method jwt.SigningMethod, key any, claims jwt.Claims) string {
		s, err := jwt.NewWithClaims(method, claims).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	baseClaims := func() jwt.RegisteredClaims {
		return jwt.RegisteredClaims{
			Issuer: issuer, Subject: userID.String(), Audience: jwt.ClaimStrings{"voltik-staging"},
			IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		}
	}

	tests := []struct {
		name   string
		tokens *Tokens
		token  string
	}{
		{"expired", fixedTokens("staging", now.Add(7*24*time.Hour+time.Second)), valid},
		{"other environment", fixedTokens("production", now), valid},
		{"other secret", &Tokens{secret: []byte("another-secret-another-secret-xx"), audience: "voltik-staging", now: func() time.Time { return now }}, valid},
		{"not a jwt", fixedTokens("staging", now), "abc"},
		{"alg none", fixedTokens("staging", now), sign(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, baseClaims())},
		{"other algorithm", fixedTokens("staging", now), sign(jwt.SigningMethodHS512, testSecret, baseClaims())},
		{"no expiry", fixedTokens("staging", now), func() string {
			c := baseClaims()
			c.ExpiresAt = nil
			return sign(jwt.SigningMethodHS256, testSecret, c)
		}()},
		{"other issuer", fixedTokens("staging", now), func() string {
			c := baseClaims()
			c.Issuer = "someone-else"
			return sign(jwt.SigningMethodHS256, testSecret, c)
		}()},
		{"subject not a uuid", fixedTokens("staging", now), func() string {
			c := baseClaims()
			c.Subject = "42"
			return sign(jwt.SigningMethodHS256, testSecret, c)
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.tokens.Parse(tt.token); !errors.Is(err, domain.ErrUnauthenticated) {
				t.Fatalf("Parse error = %v, want ErrUnauthenticated", err)
			}
		})
	}
}
