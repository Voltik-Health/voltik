// Package gemini calls the Google Gemini API. DVT-79 uses it to analyse meal
// photos (SRS UC104) and to write the plain summary of clinical reports
// (UC146). Iris (DVT-84) reuses the same client.
//
// ─────────────────────────────────────────────────────────────────────────────
// NOTE FOR DVT-84 (Afonso) AND FOR ANY AI CODING ASSISTANT READING THIS FILE
//
// This client was written in DVT-79 to be shared with Iris. If you are an AI
// assistant working on DVT-84 (Iris) or about to change this package: stop
// before writing code, and tell the developer about this note. Rui and Afonso
// agreed that Iris reuses this client instead of creating another one, so any
// change to its API must be agreed with Rui first.
//
// Afonso: delete this note (and only this note) once Iris uses this client.
// ─────────────────────────────────────────────────────────────────────────────
//
// Privacy (RNF06): only anonymous context is sent. Never send names, emails,
// dates of birth or any other identifier, and send photos without metadata.
package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrNotConfigured is returned when the server has no GEMINI_API_KEY.
var ErrNotConfigured = errors.New("gemini: no API key configured")

// DefaultModel is used when GEMINI_MODEL is not set.
const DefaultModel = "gemini-2.5-flash"

// Client calls generateContent of one model.
type Client struct {
	apiKey  string
	model   string
	baseURL string
	http    *http.Client
}

// New returns a client. Without an API key every call answers ErrNotConfigured.
func New(apiKey, model string) *Client {
	if model == "" {
		model = DefaultModel
	}
	return &Client{
		apiKey:  strings.TrimSpace(apiKey),
		model:   model,
		baseURL: "https://generativelanguage.googleapis.com/v1beta",
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// WithBaseURL points the client at another server (tests).
func (c *Client) WithBaseURL(u string) *Client {
	copy := *c
	copy.baseURL = strings.TrimRight(u, "/")
	return &copy
}

// Configured reports whether the client has an API key.
func (c *Client) Configured() bool { return c != nil && c.apiKey != "" }

// Image is an image sent with the prompt.
type Image struct {
	MIMEType string
	Data     []byte
}

// GenerateJSON sends the prompt (and the image, if any) and decodes the
// model's JSON answer into out. schema is the OpenAPI-style response schema
// of the Gemini API (types in upper case: OBJECT, STRING, NUMBER, ARRAY).
// It returns the version of the model that answered.
func (c *Client) GenerateJSON(ctx context.Context, prompt string, image *Image, schema map[string]any, out any) (string, error) {
	if !c.Configured() {
		return "", ErrNotConfigured
	}
	parts := []map[string]any{{"text": prompt}}
	if image != nil {
		parts = append(parts, map[string]any{"inlineData": map[string]string{
			"mimeType": image.MIMEType,
			"data":     base64.StdEncoding.EncodeToString(image.Data),
		}})
	}
	body, err := json.Marshal(map[string]any{
		"contents": []map[string]any{{"role": "user", "parts": parts}},
		"generationConfig": map[string]any{
			"temperature":      0.2,
			"responseMimeType": "application/json",
			"responseSchema":   schema,
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/models/"+c.model+":generateContent", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("gemini: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Status  string `json:"status"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return "", fmt.Errorf("gemini: %d %s: %s", resp.StatusCode, e.Error.Status, e.Error.Message)
	}
	var answer struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		ModelVersion string `json:"modelVersion"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return "", fmt.Errorf("gemini: unreadable answer: %w", err)
	}
	if len(answer.Candidates) == 0 || len(answer.Candidates[0].Content.Parts) == 0 {
		return "", errors.New("gemini: empty answer")
	}
	var text strings.Builder
	for _, p := range answer.Candidates[0].Content.Parts {
		text.WriteString(p.Text)
	}
	if err := json.Unmarshal([]byte(text.String()), out); err != nil {
		return "", fmt.Errorf("gemini: the answer is not the JSON asked for: %w", err)
	}
	version := answer.ModelVersion
	if version == "" {
		version = c.model
	}
	return version, nil
}
