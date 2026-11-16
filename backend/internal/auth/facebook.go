package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/voltik/voltik/backend/internal/domain"
)

// FacebookGraphURL is the Graph API. Paths are left unversioned so they follow
// the version set in the Meta app and never expire.
const FacebookGraphURL = "https://graph.facebook.com"

// FacebookVerifier checks a Facebook Login user access token with the Graph
// API, because Facebook gives no ID token on Android and the web.
type FacebookVerifier struct {
	appID     string
	appSecret string
	graphURL  string
	client    *http.Client
}

func NewFacebookVerifier(appID, appSecret, graphURL string) *FacebookVerifier {
	return &FacebookVerifier{
		appID:     appID,
		appSecret: appSecret,
		graphURL:  strings.TrimSuffix(graphURL, "/"),
		client:    &http.Client{Timeout: 5 * time.Second},
	}
}

// Verify asks Facebook whether the token is valid and was issued to the Voltik
// app (debug_token), then reads the user's name and email (/me).
func (f *FacebookVerifier) Verify(ctx context.Context, token string) (domain.Identity, error) {
	var debug struct {
		Data struct {
			AppID   string `json:"app_id"`
			IsValid bool   `json:"is_valid"`
			UserID  string `json:"user_id"`
		} `json:"data"`
	}
	err := f.get(ctx, "/debug_token", url.Values{
		"input_token":  {token},
		"access_token": {f.appID + "|" + f.appSecret},
	}, &debug)
	if err != nil {
		return domain.Identity{}, err
	}
	if !debug.Data.IsValid || debug.Data.AppID != f.appID || debug.Data.UserID == "" {
		return domain.Identity{}, invalidToken(fmt.Errorf("facebook token not valid for this app"))
	}

	var me struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"` // only sent when the user granted it
	}
	mac := hmac.New(sha256.New, []byte(f.appSecret))
	mac.Write([]byte(token))
	err = f.get(ctx, "/me", url.Values{
		"fields":          {"id,name,email"},
		"access_token":    {token},
		"appsecret_proof": {hex.EncodeToString(mac.Sum(nil))},
	}, &me)
	if err != nil {
		return domain.Identity{}, err
	}
	if me.ID != debug.Data.UserID {
		return domain.Identity{}, invalidToken(fmt.Errorf("facebook /me answered for another user"))
	}

	id := domain.Identity{Provider: domain.ProviderFacebook, Subject: me.ID, FullName: strings.TrimSpace(me.Name)}
	if me.Email != "" {
		id.Email = &me.Email // Facebook only shares confirmed emails
	}
	return id, nil
}

// get calls the Graph API. Its errors never include the URL, which holds the tokens.
func (f *FacebookVerifier) get(ctx context.Context, path string, query url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.graphURL+path+"?"+query.Encode(), nil)
	if err != nil {
		return fmt.Errorf("%w: facebook request", domain.ErrServiceUnavailable)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: facebook graph api not reachable", domain.ErrServiceUnavailable)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: facebook graph api answered %d", domain.ErrServiceUnavailable, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return invalidToken(fmt.Errorf("facebook graph api answered %d", resp.StatusCode))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return invalidToken(fmt.Errorf("facebook graph api answer: %v", err))
	}
	return nil
}
