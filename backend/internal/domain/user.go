// Package domain holds the business types and errors shared by every layer.
// It depends on nothing else in the project.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// RetentionPeriod is how long a deleted account is kept, deactivated, before
// the daily job erases it for good (GDPR, ADR 0006).
const RetentionPeriod = 30 * 24 * time.Hour

// DefaultFullName is stored when the identity provider shares no name.
// The user can change it with PATCH /v1/me.
const DefaultFullName = "Voltik user"

// AccountStatus is users.account_status.
type AccountStatus string

const (
	AccountActive      AccountStatus = "active"
	AccountSuspended   AccountStatus = "suspended"
	AccountPending     AccountStatus = "pending"
	AccountDeactivated AccountStatus = "deactivated"
)

// Role says which area of the apps a user can open. Roles are not stored:
// they are derived from the data on every request (SRS 3.1).
type Role string

const (
	RolePatient            Role = "patient"             // live row in patients
	RoleCaregiver          Role = "caregiver"           // active caregiver_links
	RoleHealthProfessional Role = "health_professional" // live row in health_professionals
	RoleCompanyManager     Role = "company_manager"     // row in organization_managers
	RoleAdmin              Role = "admin"               // users.is_platform_admin
)

// User is a row of users.
type User struct {
	ID              uuid.UUID
	Email           *string
	FullName        string
	Phone           *string
	ProfilePhotoURL *string
	AccountStatus   AccountStatus
	CreatedAt       time.Time
	DeletedAt       *time.Time
}

// Account is a user together with what they can do right now.
type Account struct {
	User
	Roles     []Role     // in the order of the constants above
	PatientID *uuid.UUID // the user's live patient profile, if any
}

// HasRole reports whether the account has the role.
func (a Account) HasRole(r Role) bool {
	for _, have := range a.Roles {
		if have == r {
			return true
		}
	}
	return false
}

// Limits of the users table.
const (
	maxFullNameLength = 150
	maxPhoneLength    = 30
)

// ValidateProfile checks the fields the user can edit.
func (u User) ValidateProfile() error {
	var v ValidationError
	checkText(&v, "full_name", u.FullName, maxFullNameLength)
	if u.Phone != nil {
		checkText(&v, "phone", *u.Phone, maxPhoneLength)
	}
	return v.OrNil()
}

// Session is what a successful sign-in returns.
type Session struct {
	Token     string
	ExpiresAt time.Time
	Account   Account
}

// EraseAt is when the retention job erases a deleted account.
func EraseAt(deletedAt time.Time) time.Time {
	return deletedAt.Add(RetentionPeriod)
}
