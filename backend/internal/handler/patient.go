package handler

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/voltik/voltik/backend/internal/api"
	"github.com/voltik/voltik/backend/internal/domain"
)

func (s *Server) GetPatientProfile(ctx context.Context, _ api.GetPatientProfileRequestObject) (api.GetPatientProfileResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.patients.Get(ctx, a)
	if err != nil {
		return nil, err
	}
	return api.GetPatientProfile200JSONResponse(toPatient(p)), nil
}

func (s *Server) CreatePatientProfile(ctx context.Context, req api.CreatePatientProfileRequestObject) (api.CreatePatientProfileResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body

	var v domain.ValidationError
	if b.BirthDate.Time.IsZero() {
		v.Add("birth_date", "is required")
	}
	if !b.DiabetesType.Valid() {
		v.Add("diabetes_type", "is required and must be one of the listed values")
	}
	checkEnum(&v, "gender", b.Gender)
	checkEnum(&v, "current_therapy", b.CurrentTherapy)
	consents := make([]domain.ConsentRequest, 0, len(b.Consents))
	for i, c := range b.Consents {
		consents = append(consents, consentRequest(&v, fmt.Sprintf("consents[%d].", i), c))
	}
	if err := v.OrNil(); err != nil {
		return nil, err
	}

	p := domain.Patient{
		BirthDate:    b.BirthDate.Time,
		Gender:       stringPtr(b.Gender),
		Occupation:   b.Occupation,
		DiabetesType: string(b.DiabetesType),
		Therapy:      stringPtr(b.CurrentTherapy),
		Thresholds:   domain.DefaultThresholds,
		WeightKg:     b.WeightKg,
		HeightCm:     b.HeightCm,
	}
	setIfSent(&p.TargetMin, b.TargetMinMgDl)
	setIfSent(&p.TargetMax, b.TargetMaxMgDl)
	setIfSent(&p.CriticalLow, b.CriticalLowMgDl)
	setIfSent(&p.CriticalHigh, b.CriticalHighMgDl)

	created, err := s.patients.Create(ctx, a, p, consents, requestMeta(ctx))
	if err != nil {
		return nil, err
	}
	return api.CreatePatientProfile201JSONResponse(toPatient(created)), nil
}

func (s *Server) UpdatePatientProfile(ctx context.Context, req api.UpdatePatientProfileRequestObject) (api.UpdatePatientProfileResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if b.BirthDate == nil && !b.Gender.IsSpecified() && !b.Occupation.IsSpecified() && b.DiabetesType == nil &&
		!b.CurrentTherapy.IsSpecified() && b.TargetMinMgDl == nil && b.TargetMaxMgDl == nil &&
		b.CriticalLowMgDl == nil && b.CriticalHighMgDl == nil && !b.WeightKg.IsSpecified() && !b.HeightCm.IsSpecified() {
		return nil, &badRequest{message: "Send at least one field to change."}
	}

	var v domain.ValidationError
	if b.DiabetesType != nil && !b.DiabetesType.Valid() {
		v.Add("diabetes_type", "must be one of the listed values")
	}
	if b.Gender.IsSpecified() && !b.Gender.IsNull() {
		checkEnum(&v, "gender", ptr(b.Gender.MustGet()))
	}
	if b.CurrentTherapy.IsSpecified() && !b.CurrentTherapy.IsNull() {
		checkEnum(&v, "current_therapy", ptr(b.CurrentTherapy.MustGet()))
	}
	if err := v.OrNil(); err != nil {
		return nil, err
	}

	updated, err := s.patients.Update(ctx, a, func(p *domain.Patient) {
		if b.BirthDate != nil {
			p.BirthDate = b.BirthDate.Time
		}
		patchString(b.Gender, &p.Gender)
		patchString(b.Occupation, &p.Occupation)
		if b.DiabetesType != nil {
			p.DiabetesType = string(*b.DiabetesType)
		}
		patchString(b.CurrentTherapy, &p.Therapy)
		setIfSent(&p.TargetMin, b.TargetMinMgDl)
		setIfSent(&p.TargetMax, b.TargetMaxMgDl)
		setIfSent(&p.CriticalLow, b.CriticalLowMgDl)
		setIfSent(&p.CriticalHigh, b.CriticalHighMgDl)
		patchValue(b.WeightKg, &p.WeightKg)
		patchValue(b.HeightCm, &p.HeightCm)
	})
	if err != nil {
		return nil, err
	}
	return api.UpdatePatientProfile200JSONResponse(toPatient(updated)), nil
}

// consentRequest checks one consent of a request; prefix names its position.
func consentRequest(v *domain.ValidationError, prefix string, c api.ConsentAcceptance) domain.ConsentRequest {
	if !c.ConsentType.Valid() {
		v.Add(prefix+"consent_type", "must be one of the listed values")
	}
	version := strings.TrimSpace(c.DocumentVersion)
	if version == "" || utf8.RuneCountInString(version) > 20 {
		v.Add(prefix+"document_version", "must have between 1 and 20 characters")
	}
	return domain.ConsentRequest{DocumentType: string(c.ConsentType), Version: version}
}

// enum is a generated enumeration type of the contract.
type enum interface {
	~string
	Valid() bool
}

func checkEnum[T enum](v *domain.ValidationError, field string, value *T) {
	if value != nil && !(*value).Valid() {
		v.Add(field, "must be one of the listed values")
	}
}

func stringPtr[T ~string](v *T) *string {
	if v == nil {
		return nil
	}
	s := string(*v)
	return &s
}

func setIfSent(target *int, value *int) {
	if value != nil {
		*target = *value
	}
}

func ptr[T any](v T) *T { return &v }
