package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/voltik/voltik/backend/internal/api"
	"github.com/voltik/voltik/backend/internal/database"
	"github.com/voltik/voltik/backend/internal/domain"
)

// badRequest is a request the API cannot read: malformed JSON, wrong types,
// or nothing to change in a PATCH.
type badRequest struct{ message string }

func (e *badRequest) Error() string { return e.message }

// errorResponse turns an error into the status and the body of the contract
// (`code` values: see the table in api/openapi.yaml).
func errorResponse(err error) (int, api.Error) {
	var (
		bad      *badRequest
		invalid  *domain.ValidationError
		pending  *domain.PendingDeletionError
		outdated *domain.OutdatedDocumentError
	)
	status, code, message := http.StatusInternalServerError, "internal_error", "Unexpected server error."
	details := []api.ErrorDetail{}
	switch {
	case errors.As(err, &bad):
		status, code, message = http.StatusBadRequest, "invalid_request", bad.message
	case errors.As(err, &invalid):
		status, code, message = http.StatusBadRequest, "validation_failed", "Some fields are not valid."
		for _, i := range invalid.Issues {
			details = append(details, api.ErrorDetail{Field: i.Field, Issue: i.Issue})
		}
	case errors.Is(err, domain.ErrUnauthenticated):
		status, code, message = http.StatusUnauthorized, "unauthenticated", "The session token is missing, invalid or expired."
	case errors.Is(err, domain.ErrInvalidProviderToken):
		status, code, message = http.StatusUnauthorized, "invalid_provider_token", "The identity provider rejected the token."
	case errors.Is(err, domain.ErrAccountDisabled):
		status, code, message = http.StatusForbidden, "account_disabled", "This account is suspended."
	case errors.As(err, &pending):
		status, code, message = http.StatusForbidden, "account_pending_deletion",
			"This account will be erased on "+pending.EraseAt.UTC().Format("2006-01-02T15:04:05Z")+"."
	case errors.Is(err, domain.ErrNotFound):
		status, code, message = http.StatusNotFound, "not_found", "Not found."
	case errors.Is(err, domain.ErrPatientProfileNotFound):
		status, code, message = http.StatusNotFound, "patient_profile_not_found", "Complete onboarding with POST /v1/me/patient first."
	case errors.Is(err, domain.ErrEmailInUse):
		status, code, message = http.StatusConflict, "email_in_use", "This email already has an account that signs in with another provider."
	case errors.Is(err, domain.ErrPatientProfileExists):
		status, code, message = http.StatusConflict, "patient_profile_exists", "This user already has a patient profile."
	case errors.Is(err, domain.ErrMandatoryConsent):
		status, code, message = http.StatusConflict, "mandatory_consent", "Mandatory consents cannot be revoked. Delete the account instead."
	case errors.As(err, &outdated):
		status, code, message = http.StatusConflict, "document_outdated",
			"Version "+outdated.Version+" of "+outdated.DocumentType+" is not the version in force."
	case errors.Is(err, domain.ErrProfessionalHasPatients):
		status, code, message = http.StatusConflict, "professional_has_patients", "End the active follow-ups before deleting this account."
	case errors.Is(err, domain.ErrProviderUnavailable):
		status, code, message = http.StatusServiceUnavailable, "provider_unavailable", "Sign-in with this identity provider is not available."
	case errors.Is(err, domain.ErrServiceUnavailable), database.IsUnavailable(err):
		status, code, message = http.StatusServiceUnavailable, "service_unavailable", "A service the API depends on is not reachable."
	}
	return status, api.Error{Code: code, Message: message, Details: details}
}

// writeError answers with the contract's error body, and logs server errors.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, body := errorResponse(err)
	switch {
	case status == http.StatusInternalServerError:
		s.log.Error("request failed", "request_id", requestID(r.Context()), "error", err)
	case body.Code == "service_unavailable":
		s.log.Warn("dependency not reachable", "request_id", requestID(r.Context()), "error", err)
	case status == http.StatusUnauthorized:
		w.Header().Set("WWW-Authenticate", `Bearer realm="voltik"`)
	}
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
