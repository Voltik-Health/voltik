package repository

import (
	"encoding/base64"
	"strings"
	"time"

	"github.com/voltik/voltik/backend/internal/domain"
)

// A cursor is the position after the last item of a page: its time and its
// id, opaque to the apps (keyset pagination).
func encodeCursor(t time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(t.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeCursor(cursor string) (time.Time, string, error) {
	invalid := domain.Invalid("cursor", "is not a cursor returned by the API")
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", invalid
	}
	at, id, ok := strings.Cut(string(raw), "|")
	if !ok || id == "" {
		return time.Time{}, "", invalid
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, "", invalid
	}
	return t, id, nil
}
