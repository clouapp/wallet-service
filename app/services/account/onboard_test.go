package account_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

const onboardPasswordHash = "stored-password-hash"

func TestOnboard_Rolls_BackAccountAndMembershipWhenMembershipInsertFails(t *testing.T) {
	state := newMemState()
	state.failMembership = true
	ctx := context.Background()
	email := "onboard-rollback-" + uuid.NewString() + "@example.com"
	organization := "Onboard Rollback " + uuid.NewString()
	users := memUsers{state: state}
	accounts := memAccounts{state: state}
	memberships := memMemberships{state: state}
	svc := accountsvc.NewService(accountsvc.Deps{
		Accounts:    accounts,
		Memberships: memberships,
		Users:       users,
	})

	dispatched := 0
	user, err := svc.Onboard(ctx, accountsvc.OnboardInput{
		Email:            email,
		PasswordHash:     onboardPasswordHash,
		FullName:         "Rollback User",
		OrganizationName: organization,
	}, func(uuid.UUID) error {
		dispatched++
		return nil
	})
	if err == nil || user != nil || !strings.Contains(err.Error(), "membership insert failed") {
		t.Fatalf("membership insert did not fail the register: %v", err)
	}
	if dispatched != 0 {
		t.Fatal("mail was dispatched after the transaction rolled back")
	}
	if count := state.countUsers(email); count != 0 {
		t.Fatalf("user rows = %d", count)
	}
	if count := state.countAccounts(organization, organization+" [test]"); count != 0 {
		t.Fatalf("account rows = %d", count)
	}
	if count := state.countMembers(uuid.Nil, "", ""); count != 0 {
		t.Fatalf("membership rows = %d", count)
	}
}

func TestOnboard_Commits_ThenDispatchesMail(t *testing.T) {
	state := newMemState()
	ctx := context.Background()
	email := "onboard-commit-" + uuid.NewString() + "@example.com"
	organization := "Onboard Commit " + uuid.NewString()
	users := memUsers{state: state}
	accounts := memAccounts{state: state}
	memberships := memMemberships{state: state}
	svc := accountsvc.NewService(accountsvc.Deps{
		Accounts:    accounts,
		Memberships: memberships,
		Users:       users,
	})

	dispatched := 0
	user, err := svc.Onboard(ctx, accountsvc.OnboardInput{
		Email:            email,
		PasswordHash:     onboardPasswordHash,
		FullName:         "Commit User",
		OrganizationName: organization,
	}, func(userID uuid.UUID) error {
		stored, findErr := users.FindByID(context.Background(), userID)
		if findErr != nil || stored == nil || stored.Email != email {
			t.Fatal("mail was dispatched before the user was committed")
		}
		if stored.PasswordHash != onboardPasswordHash {
			t.Fatal("committed user is missing the password hash")
		}
		if count := state.countAccounts(organization, organization+" [test]"); count != 2 {
			t.Fatalf("accounts visible to mail = %d", count)
		}
		if count := state.countMembers(userID, "", ""); count != 2 {
			t.Fatalf("memberships visible to mail = %d", count)
		}
		dispatched++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if user == nil || user.ID == uuid.Nil || user.DefaultAccountID == nil {
		t.Fatal("onboard did not return the user")
	}
	if dispatched != 1 {
		t.Fatalf("mail dispatches = %d", dispatched)
	}

	prod, err := accounts.FindByID(ctx, *user.DefaultAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if prod.Name != organization || prod.Environment != models.EnvironmentProd || prod.LinkedAccountID == nil {
		t.Fatalf("production account = %+v", prod)
	}
	testAccount, err := accounts.FindByID(ctx, *prod.LinkedAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if testAccount.Name != organization+" [test]" || testAccount.Environment != models.EnvironmentTest || testAccount.LinkedAccountID == nil || *testAccount.LinkedAccountID != prod.ID {
		t.Fatalf("test account = %+v", testAccount)
	}
	if count := state.countMembers(user.ID, "owner", models.MembershipStatusActive); count != 2 {
		t.Fatalf("owner memberships = %d", count)
	}
}
