package domain

import "net/netip"

// Identity providers, as in identity_providers.code.
const (
	ProviderGoogle    = "google"
	ProviderMicrosoft = "microsoft"
	ProviderApple     = "apple"
	ProviderFacebook  = "facebook"
)

// Identity is who an identity provider says the user is, after the API
// checked the provider's token.
type Identity struct {
	Provider string
	Subject  string  // the provider's stable user id ("sub")
	Email    *string // only when the provider verified it
	FullName string  // empty when the provider shared none
	PhotoURL *string
}

// RequestMeta is where a request came from, for consents and the audit log.
type RequestMeta struct {
	IP        netip.Addr
	UserAgent string
}

// Operations written to data_access_audit_log (SRS UC021).
const (
	AuditConsentGranted         = "consent_granted"
	AuditConsentRevoked         = "consent_revoked"
	AuditAccountDeletionRequest = "account_deletion_requested"
)
