package handler

import (
	"time"

	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/voltik/voltik/backend/internal/api"
	"github.com/voltik/voltik/backend/internal/domain"
)

// nullableOf turns an optional value into a field that is always present in
// the JSON, as null when there is no value (contract convention).
func nullableOf[T any](v *T) nullable.Nullable[T] {
	if v == nil {
		return nullable.NewNullNullable[T]()
	}
	return nullable.NewNullableWithValue(*v)
}

// nullableAs is nullableOf for a string kept in a named string type (enums, email).
func nullableAs[T ~string](v *string) nullable.Nullable[T] {
	if v == nil {
		return nullable.NewNullNullable[T]()
	}
	return nullable.NewNullableWithValue(T(*v))
}

// patchString applies a nullable PATCH field to an optional value: left out
// keeps it, null clears it, a value replaces it.
func patchString[T ~string](field nullable.Nullable[T], target **string) {
	if !field.IsSpecified() {
		return
	}
	if field.IsNull() {
		*target = nil
		return
	}
	v := string(field.MustGet())
	*target = &v
}

func patchValue[T any](field nullable.Nullable[T], target **T) {
	if !field.IsSpecified() {
		return
	}
	if field.IsNull() {
		*target = nil
		return
	}
	v := field.MustGet()
	*target = &v
}

func toMe(a domain.Account) api.Me {
	roles := make([]api.ProfileType, 0, len(a.Roles))
	for _, r := range a.Roles {
		roles = append(roles, api.ProfileType(r))
	}
	return api.Me{
		Id:              a.ID,
		Email:           nullableAs[openapi_types.Email](a.Email),
		FullName:        a.FullName,
		Phone:           nullableOf(a.Phone),
		ProfilePhotoUrl: nullableOf(a.ProfilePhotoURL),
		AccountStatus:   api.AccountStatus(a.AccountStatus),
		ProfileTypes:    roles,
		CreatedAt:       a.CreatedAt.UTC(),
	}
}

func toSession(s domain.Session) api.Session {
	return api.Session{
		AccessToken: s.Token,
		TokenType:   "Bearer",
		ExpiresAt:   s.ExpiresAt.UTC(),
		User:        toMe(s.Account),
	}
}

func toPatient(p domain.Patient) api.Patient {
	return api.Patient{
		Id:               p.ID,
		BirthDate:        openapi_types.Date{Time: p.BirthDate},
		Gender:           nullableAs[api.Gender](p.Gender),
		Occupation:       nullableOf(p.Occupation),
		DiabetesType:     api.DiabetesType(p.DiabetesType),
		CurrentTherapy:   nullableAs[api.Therapy](p.Therapy),
		TargetMinMgDl:    p.TargetMin,
		TargetMaxMgDl:    p.TargetMax,
		CriticalLowMgDl:  p.CriticalLow,
		CriticalHighMgDl: p.CriticalHigh,
		WeightKg:         nullableOf(p.WeightKg),
		HeightCm:         nullableOf(p.HeightCm),
		CreatedAt:        p.CreatedAt.UTC(),
		UpdatedAt:        p.UpdatedAt.UTC(),
	}
}

func toConsent(c domain.Consent) api.Consent {
	var revoked *time.Time
	if c.RevokedAt != nil {
		t := c.RevokedAt.UTC()
		revoked = &t
	}
	return api.Consent{
		ConsentType:     api.ConsentType(c.DocumentType),
		DocumentVersion: c.Version,
		CreatedAt:       c.CreatedAt.UTC(),
		RevokedAt:       nullableOf(revoked),
	}
}
