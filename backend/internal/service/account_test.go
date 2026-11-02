package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

func TestAccountsUpdate(t *testing.T) {
	store := &fakeStore{user: domain.User{FullName: "Manuel Sousa"}}
	a := domain.Account{User: store.user, Roles: []domain.Role{domain.RolePatient}}

	got, err := NewAccounts(store).Update(context.Background(), a, func(u *domain.User) {
		u.FullName = "  Manuel A. Sousa "
		u.Phone = ptr(" +351912345678 ")
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.FullName != "Manuel A. Sousa" || *got.Phone != "+351912345678" || len(got.Roles) != 1 {
		t.Fatalf("account = %+v", got)
	}

	_, err = NewAccounts(store).Update(context.Background(), a, func(u *domain.User) { u.Phone = ptr("   ") })
	var invalid *domain.ValidationError
	if !errors.As(err, &invalid) || invalid.Issues[0].Field != "phone" {
		t.Fatalf("blank phone: err = %v", err)
	}
	if *store.user.Phone != "+351912345678" {
		t.Fatalf("an invalid change was saved: %+v", store.user)
	}
}

func TestAccountsDelete(t *testing.T) {
	patientID := uuid.New()
	patient := domain.Account{Roles: []domain.Role{domain.RolePatient}, PatientID: &patientID}
	clinician := domain.Account{Roles: []domain.Role{domain.RoleHealthProfessional}}

	store := &fakeStore{}
	if err := NewAccounts(store).Delete(context.Background(), patient, domain.RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	if !store.deletionCalled || store.deletedPatient == nil || *store.deletedPatient != patientID {
		t.Fatalf("deletion not requested for the patient: %+v", store)
	}

	busy := &fakeStore{followUps: true}
	if err := NewAccounts(busy).Delete(context.Background(), clinician, domain.RequestMeta{}); !errors.Is(err, domain.ErrProfessionalHasPatients) {
		t.Fatalf("clinician with patients: err = %v", err)
	}
	if busy.deletionCalled {
		t.Fatal("deletion requested for a clinician with patients")
	}

	free := &fakeStore{}
	if err := NewAccounts(free).Delete(context.Background(), clinician, domain.RequestMeta{}); err != nil || !free.deletionCalled {
		t.Fatalf("clinician without patients: err = %v", err)
	}
}
