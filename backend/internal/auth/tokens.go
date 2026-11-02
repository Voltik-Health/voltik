// Package auth checks who is calling: the identity providers' tokens at
// sign-in, and the Voltik session token (a JWT) on every other request.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

// SessionTTL is how long a Voltik session token is valid. There is no refresh token.
const SessionTTL = 7 * 24 * time.Hour

const issuer = "voltik-api"

// Tokens issues and checks Voltik session tokens: JWT signed with HS256.
// A token carries only the user id; the roles are read from the database on
// every request (ADR 0006).
type Tokens struct {
	secret   []byte
	audience string // the environment, so a staging token never works in production
	now      func() time.Time
}

func NewTokens(secret []byte, environment string) *Tokens {
	return &Tokens{secret: secret, audience: "voltik-" + environment, now: time.Now}
}

// Issue creates a session token for the user.
func (t *Tokens) Issue(userID uuid.UUID) (token string, expiresAt time.Time, err error) {
	now := t.now().UTC().Truncate(time.Second)
	expiresAt = now.Add(SessionTTL)
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", time.Time{}, err
	}
	claims := jwt.RegisteredClaims{
		Issuer:    issuer,
		Subject:   userID.String(),
		Audience:  jwt.ClaimStrings{t.audience},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(expiresAt),
		ID:        hex.EncodeToString(id), // lets a revocation list name the token later (UC005)
	}
	token, err = jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
	return token, expiresAt, err
}

// Parse checks the token's signature, issuer, audience and expiry, and returns the user id.
func (t *Tokens) Parse(token string) (uuid.UUID, error) {
	var claims jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(token, &claims, func(*jwt.Token) (any, error) { return t.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(t.audience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(t.now),
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: %v", domain.ErrUnauthenticated, err)
	}
	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, errors.Join(domain.ErrUnauthenticated, err)
	}
	return id, nil
}
