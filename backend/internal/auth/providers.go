package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/voltik/voltik/backend/internal/domain"
)

// The verifiers below check a token from one identity provider and say who it
// identifies. Every error wraps domain.ErrInvalidProviderToken or, when the
// provider cannot be reached, domain.ErrServiceUnavailable.

// OIDCConfig is where an OpenID Connect provider publishes its keys and who
// its tokens must be issued to.
type OIDCConfig struct {
	Issuer    string   // for Microsoft, with {tenantid} in place of the tenant
	JWKSURL   string   // the provider's public signing keys
	ClientIDs []string // accepted audiences: the Voltik apps registered with the provider
}

func GoogleConfig(clientIDs []string) OIDCConfig {
	return OIDCConfig{
		Issuer:    "https://accounts.google.com",
		JWKSURL:   "https://www.googleapis.com/oauth2/v3/certs",
		ClientIDs: clientIDs,
	}
}

// MicrosoftConfig accepts personal and work or school accounts (the "common" endpoint).
func MicrosoftConfig(clientID string) OIDCConfig {
	return OIDCConfig{
		Issuer:    "https://login.microsoftonline.com/{tenantid}/v2.0",
		JWKSURL:   "https://login.microsoftonline.com/common/discovery/v2.0/keys",
		ClientIDs: []string{clientID},
	}
}

func AppleConfig(clientIDs []string) OIDCConfig {
	return OIDCConfig{
		Issuer:    "https://appleid.apple.com",
		JWKSURL:   "https://appleid.apple.com/auth/keys",
		ClientIDs: clientIDs,
	}
}

// Longest photo URL users.profile_photo_url accepts.
const maxPhotoURLLength = 500

// Tenant of personal Microsoft accounts (outlook.com, hotmail.com, ...), whose
// email Microsoft has verified.
const microsoftConsumerTenant = "9188040d-6c67-4c5b-b112-36a304b66dad"

// oidcVerifier checks an ID token's signature, issuer, audience and expiry.
// ctx must live as long as the API: the provider's keys are fetched with it
// and cached.
type oidcVerifier struct {
	provider  string
	issuer    string
	clientIDs []string
	verifier  *oidc.IDTokenVerifier
}

func newOIDCVerifier(ctx context.Context, provider string, cfg OIDCConfig) *oidcVerifier {
	keys := oidc.NewRemoteKeySet(ctx, cfg.JWKSURL)
	return &oidcVerifier{
		provider:  provider,
		issuer:    cfg.Issuer,
		clientIDs: cfg.ClientIDs,
		verifier: oidc.NewVerifier(cfg.Issuer, keys, &oidc.Config{
			SkipClientIDCheck: true, // several client IDs: checked in verify
			SkipIssuerCheck:   strings.Contains(cfg.Issuer, "{tenantid}"),
		}),
	}
}

// claims are the ID token claims Voltik reads. Not every provider sends all of them.
type claims struct {
	Email         string   `json:"email"`
	EmailVerified flexBool `json:"email_verified"` // Google, Apple
	Name          string   `json:"name"`
	Picture       string   `json:"picture"`  // Google
	TenantID      string   `json:"tid"`      // Microsoft
	DomainOwner   flexBool `json:"xms_edov"` // Microsoft: the tenant owns the email's domain
}

func (v *oidcVerifier) verify(ctx context.Context, raw string) (domain.Identity, claims, error) {
	var c claims
	tok, err := v.verifier.Verify(ctx, raw)
	if err != nil {
		return domain.Identity{}, c, invalidToken(err)
	}
	if !slices.ContainsFunc(tok.Audience, func(aud string) bool { return slices.Contains(v.clientIDs, aud) }) {
		return domain.Identity{}, c, invalidToken(fmt.Errorf("token issued to %v", tok.Audience))
	}
	if err := tok.Claims(&c); err != nil {
		return domain.Identity{}, c, invalidToken(err)
	}
	if strings.Contains(v.issuer, "{tenantid}") {
		if c.TenantID == "" || tok.Issuer != strings.Replace(v.issuer, "{tenantid}", c.TenantID, 1) {
			return domain.Identity{}, c, invalidToken(fmt.Errorf("unexpected issuer %q", tok.Issuer))
		}
	}
	return domain.Identity{Provider: v.provider, Subject: tok.Subject, FullName: strings.TrimSpace(c.Name)}, c, nil
}

// GoogleVerifier checks Google ID tokens (Credential Manager, Google Identity Services).
type GoogleVerifier struct{ *oidcVerifier }

func NewGoogleVerifier(ctx context.Context, cfg OIDCConfig) *GoogleVerifier {
	return &GoogleVerifier{newOIDCVerifier(ctx, domain.ProviderGoogle, cfg)}
}

func (g *GoogleVerifier) Verify(ctx context.Context, raw string) (domain.Identity, error) {
	id, c, err := g.verify(ctx, raw)
	if err != nil {
		return id, err
	}
	if c.Email != "" && bool(c.EmailVerified) {
		id.Email = &c.Email
	}
	if c.Picture != "" && len(c.Picture) <= maxPhotoURLLength {
		id.PhotoURL = &c.Picture
	}
	return id, nil
}

// MicrosoftVerifier checks Microsoft ID tokens (MSAL).
type MicrosoftVerifier struct{ *oidcVerifier }

func NewMicrosoftVerifier(ctx context.Context, cfg OIDCConfig) *MicrosoftVerifier {
	return &MicrosoftVerifier{newOIDCVerifier(ctx, domain.ProviderMicrosoft, cfg)}
}

// Verify keeps the email only when Microsoft vouches for it: personal accounts,
// or work accounts whose organisation owns the email's domain (xms_edov).
// Otherwise anyone could put someone else's address on a work account.
func (m *MicrosoftVerifier) Verify(ctx context.Context, raw string) (domain.Identity, error) {
	id, c, err := m.verify(ctx, raw)
	if err != nil {
		return id, err
	}
	if c.Email != "" && (c.TenantID == microsoftConsumerTenant || bool(c.DomainOwner)) {
		id.Email = &c.Email
	}
	return id, nil
}

// AppleVerifier checks Sign in with Apple identity tokens. Apple never puts the
// name in the token: the app sends it on the first sign-in.
type AppleVerifier struct{ *oidcVerifier }

func NewAppleVerifier(ctx context.Context, cfg OIDCConfig) *AppleVerifier {
	return &AppleVerifier{newOIDCVerifier(ctx, domain.ProviderApple, cfg)}
}

func (a *AppleVerifier) Verify(ctx context.Context, raw string) (domain.Identity, error) {
	id, c, err := a.verify(ctx, raw)
	if err != nil {
		return id, err
	}
	if c.Email != "" && bool(c.EmailVerified) {
		id.Email = &c.Email
	}
	return id, nil
}

func invalidToken(err error) error {
	return fmt.Errorf("%w: %v", domain.ErrInvalidProviderToken, err)
}

// flexBool reads a boolean claim sent as true or as "true" (Apple sends strings).
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	switch v := v.(type) {
	case bool:
		*b = flexBool(v)
	case string:
		*b = flexBool(v == "true")
	default:
		*b = false
	}
	return nil
}
