package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
	"github.com/voltik/voltik/backend/internal/service"
	"github.com/voltik/voltik/backend/internal/sms"
)

// The endpoints of this file are plain http.Handlers, outside the generated
// server: they are not JSON in and JSON out, and they authenticate with a
// signed link, a Nightscout secret or a provider signature instead of the
// session token (contract tags Files, Integrations and Webhooks).

// SMSReceipts applies delivery receipts of escalation texts.
type SMSReceipts interface {
	SMSStatus(ctx context.Context, providerID string, delivered bool, reason string) error
}

func (s *Server) mountManualRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/files/{file_id}", s.downloadFile)
	for _, p := range []string{"/v1/nightscout/api/v1/entries", "/v1/nightscout/api/v1/entries.json"} {
		mux.HandleFunc("POST "+p, s.nightscoutUpload)
		mux.HandleFunc("GET "+p, s.nightscoutEntries)
	}
	mux.HandleFunc("GET /v1/nightscout/api/v1/entries/sgv.json", s.nightscoutEntries)
	mux.HandleFunc("GET /v1/nightscout/api/v1/status.json", s.nightscoutStatus)
	mux.HandleFunc("GET /v1/nightscout/api/v1/status", s.nightscoutStatus)
	// Uploaders also send treatments and phone status; Voltik does not use them.
	for _, p := range []string{"/v1/nightscout/api/v1/treatments", "/v1/nightscout/api/v1/devicestatus"} {
		mux.HandleFunc("POST "+p, s.nightscoutIgnore)
	}
	mux.HandleFunc("POST /v1/webhooks/sms", s.smsWebhook)
}

// downloadFile serves a meal photo or a report PDF through a signed link (UC148).
func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("file_id"))
	if err != nil {
		s.writeError(w, r, domain.ErrNotFound)
		return
	}
	expires, err := strconv.ParseInt(r.URL.Query().Get("expires"), 10, 64)
	if err != nil {
		s.writeError(w, r, domain.ErrLinkExpired)
		return
	}
	f, err := s.files.Open(r.Context(), id, expires, r.URL.Query().Get("signature"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", f.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(f.Data)))
	w.Header().Set("Cache-Control", "private, max-age=900")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if f.ContentType == "application/pdf" {
		w.Header().Set("Content-Disposition", `inline; filename="voltik-relatorio.pdf"`)
	}
	_, _ = w.Write(f.Data)
}

// --- Nightscout API (Juggluco, xDrip+) -----------------------------------------------

type nightscoutEntry struct {
	Type       string `json:"type"`
	SGV        *int   `json:"sgv,omitempty"`
	MBG        *int   `json:"mbg,omitempty"`
	Date       int64  `json:"date"`
	DateString string `json:"dateString,omitempty"`
	Direction  string `json:"direction,omitempty"`
	Device     string `json:"device,omitempty"`
}

func (s *Server) nightscoutDevice(w http.ResponseWriter, r *http.Request) (domain.Device, bool) {
	secret := r.Header.Get("api-secret")
	if secret == "" {
		secret = r.URL.Query().Get("token")
	}
	d, err := s.healthData.NightscoutDevice(r.Context(), secret)
	if err != nil {
		s.writeError(w, r, err)
		return d, false
	}
	return d, true
}

func (s *Server) nightscoutUpload(w http.ResponseWriter, r *http.Request) {
	d, ok := s.nightscoutDevice(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var entries []nightscoutEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		var one nightscoutEntry // some uploaders send a single object
		if err := json.Unmarshal(raw, &one); err != nil {
			s.writeError(w, r, &badRequest{message: "Send a JSON array of Nightscout entries."})
			return
		}
		entries = []nightscoutEntry{one}
	}
	if len(entries) > 1000 {
		s.writeError(w, r, &badRequest{message: "Send at most 1000 entries."})
		return
	}
	in := make([]service.NightscoutEntry, len(entries))
	for i, e := range entries {
		in[i] = service.NightscoutEntry{Type: e.Type, SGV: e.SGV, MBG: e.MBG, DateMillis: e.Date, DateString: e.DateString, Direction: e.Direction, Device: e.Device}
	}
	accepted, err := s.healthData.IngestNightscout(r.Context(), d, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, fromNightscout(accepted))
}

func (s *Server) nightscoutEntries(w http.ResponseWriter, r *http.Request) {
	d, ok := s.nightscoutDevice(w, r)
	if !ok {
		return
	}
	count := 10
	if c := r.URL.Query().Get("count"); c != "" {
		n, err := strconv.Atoi(c)
		if err != nil || n < 1 || n > 1000 {
			s.writeError(w, r, domain.Invalid("count", "must be between 1 and 1000"))
			return
		}
		count = n
	}
	entries, err := s.healthData.NightscoutEntries(r.Context(), d, count)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, fromNightscout(entries))
}

func fromNightscout(in []service.NightscoutEntry) []nightscoutEntry {
	out := make([]nightscoutEntry, len(in))
	for i, e := range in {
		out[i] = nightscoutEntry{Type: e.Type, SGV: e.SGV, MBG: e.MBG, Date: e.DateMillis, DateString: e.DateString, Direction: e.Direction, Device: e.Device}
	}
	return out
}

func (s *Server) nightscoutStatus(w http.ResponseWriter, _ *http.Request) {
	now := time.Now().UTC()
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "name": "Voltik", "version": s.version,
		"serverTime": now.Format("2006-01-02T15:04:05.000Z"), "serverTimeEpoch": now.UnixMilli(),
		"apiEnabled": true, "careportalEnabled": false, "settings": map[string]string{"units": "mg/dl"},
	})
}

func (s *Server) nightscoutIgnore(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.nightscoutDevice(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, []any{})
}

// --- SMS delivery receipts (UC121) -----------------------------------------------------

// smsWebhook applies a Twilio status callback after checking its signature.
func (s *Server) smsWebhook(w http.ResponseWriter, r *http.Request) {
	if s.twilioAuthToken == "" || s.smsReceipts == nil {
		s.writeError(w, r, domain.ErrNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.writeError(w, r, &badRequest{message: "The form is not valid."})
		return
	}
	fullURL := s.publicURL + r.URL.RequestURI()
	if !sms.ValidTwilioSignature(s.twilioAuthToken, fullURL, r.PostForm, r.Header.Get("X-Twilio-Signature")) {
		s.writeError(w, r, domain.ErrForbidden)
		return
	}
	id, status := r.PostForm.Get("MessageSid"), strings.ToLower(r.PostForm.Get("MessageStatus"))
	var err error
	switch status {
	case "delivered":
		err = s.smsReceipts.SMSStatus(r.Context(), id, true, "")
	case "failed", "undelivered":
		err = s.smsReceipts.SMSStatus(r.Context(), id, false, "provider: "+status+" "+r.PostForm.Get("ErrorCode"))
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
