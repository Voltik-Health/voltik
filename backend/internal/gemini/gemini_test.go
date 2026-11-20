package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGenerateJSON(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "key" || r.URL.Path != "/models/gemini-test:generateContent" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"status":"PERMISSION_DENIED","message":"bad key"}}`))
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"{\"summary\":\"Olá\"}"}]}}],"modelVersion":"gemini-test-001"}`))
	}))
	defer srv.Close()

	var out struct {
		Summary string `json:"summary"`
	}
	c := New("key", "gemini-test").WithBaseURL(srv.URL)
	version, err := c.GenerateJSON(context.Background(), "prompt", &Image{MIMEType: "image/jpeg", Data: []byte{1, 2}}, map[string]any{"type": "OBJECT"}, &out)
	if err != nil || out.Summary != "Olá" || version != "gemini-test-001" {
		t.Fatalf("out=%+v version=%q err=%v", out, version, err)
	}
	parts := body["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	if len(parts) != 2 || parts[1].(map[string]any)["inlineData"] == nil {
		t.Fatalf("the image was not sent: %v", parts)
	}
	if cfg := body["generationConfig"].(map[string]any); cfg["responseMimeType"] != "application/json" {
		t.Fatalf("generationConfig = %v", cfg)
	}

	if _, err := New("wrong", "gemini-test").WithBaseURL(srv.URL).GenerateJSON(context.Background(), "p", nil, nil, &out); err == nil {
		t.Fatal("an API error must be returned")
	}
	if _, err := New("", "").GenerateJSON(context.Background(), "p", nil, nil, &out); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("without a key: %v", err)
	}
}
