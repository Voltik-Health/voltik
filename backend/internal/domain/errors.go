package domain

import (
	"errors"
	"fmt"
	"time"
)

// Errors the handlers turn into the error codes of the API contract.
var (
	ErrNotFound                = errors.New("not found")
	ErrUnauthenticated         = errors.New("session token missing, invalid or expired")
	ErrInvalidProviderToken    = errors.New("the identity provider rejected the token")
	ErrProviderUnavailable     = errors.New("sign-in with this identity provider is not available")
	ErrAccountDisabled         = errors.New("the account is suspended or deactivated")
	ErrEmailInUse              = errors.New("the email already has an account under another provider")
	ErrPatientProfileNotFound  = errors.New("patient onboarding not completed")
	ErrPatientProfileExists    = errors.New("patient onboarding already completed")
	ErrMandatoryConsent        = errors.New("mandatory consents cannot be revoked")
	ErrProfessionalHasPatients = errors.New("the clinician has active follow-ups")
	ErrServiceUnavailable      = errors.New("a dependency is not reachable")

	// ErrIdentityTaken means another request created the same provider identity
	// first. The sign-in retries and finds it.
	ErrIdentityTaken = errors.New("provider identity already exists")
)

// PendingDeletionError is returned when an account waiting for erasure tries to sign in.
type PendingDeletionError struct {
	EraseAt time.Time
}

func (e *PendingDeletionError) Error() string {
	return "this account will be erased on " + e.EraseAt.UTC().Format(time.RFC3339)
}

// OutdatedDocumentError is returned when a consent names a published version
// that is not the one in force.
type OutdatedDocumentError struct {
	DocumentType string
	Version      string
}

func (e *OutdatedDocumentError) Error() string {
	return fmt.Sprintf("version %s of %s is not the version in force", e.Version, e.DocumentType)
}

// FieldIssue is one field that breaks a rule.
type FieldIssue struct {
	Field string
	Issue string
}

// ValidationError lists every field of a request that breaks a rule.
type ValidationError struct {
	Issues []FieldIssue
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%d fields are not valid", len(e.Issues))
}

// Add records one more issue.
func (e *ValidationError) Add(field, issue string) {
	e.Issues = append(e.Issues, FieldIssue{Field: field, Issue: issue})
}

// OrNil returns the error only when it has issues.
func (e *ValidationError) OrNil() error {
	if len(e.Issues) == 0 {
		return nil
	}
	return e
}

// Invalid is a validation error with a single issue.
func Invalid(field, issue string) error {
	return &ValidationError{Issues: []FieldIssue{{Field: field, Issue: issue}}}
}
