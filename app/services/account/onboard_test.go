package account_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const onboardPasswordHash = "stored-password-hash"

func TestOnboardRollsBackAccountAndMembershipWhenMembershipInsertFails(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	email := "onboard-rollback-" + uuid.NewString() + "@example.com"
	organization := "Onboard Rollback " + uuid.NewString()
	users := repositories.NewUserRepository(nil)
	accounts := repositories.NewAccountRepository(nil)
	memberships := &membershipInsertFails{
		AccountUserRepository: repositories.NewAccountUserRepository(nil),
	}
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
	if err == nil || user != nil {
		t.Fatal("membership failure returned a user")
	}
	if dispatched != 0 {
		t.Fatal("mail was dispatched after the transaction rolled back")
	}
	if memberships.userID == uuid.Nil || memberships.accountID == uuid.Nil {
		t.Fatal("membership insert was not reached after the user insert")
	}
	if count := rowCount(t, &models.User{}, "email = ?", email); count != 0 {
		t.Fatalf("user rows = %d", count)
	}
	if count := rowCount(t, &models.Account{}, "name = ? OR name = ?", organization, organization+" [test]"); count != 0 {
		t.Fatalf("account rows = %d", count)
	}
	if count := rowCount(t, &models.Account{}, "id = ?", memberships.accountID); count != 0 {
		t.Fatalf("production account rows = %d", count)
	}
	if count := rowCount(t, &models.AccountUser{}, "user_id = ? OR account_id = ?", memberships.userID, memberships.accountID); count != 0 {
		t.Fatalf("membership rows = %d", count)
	}
	if count := rowCount(t, &models.PlatformAdmin{}, "user_id = ?", memberships.userID); count != 0 {
		t.Fatalf("platform admin rows = %d", count)
	}
}

func TestOnboardCommitsThenDispatchesMail(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	email := "onboard-commit-" + uuid.NewString() + "@example.com"
	organization := "Onboard Commit " + uuid.NewString()
	users := repositories.NewUserRepository(nil)
	accounts := repositories.NewAccountRepository(nil)
	memberships := &membershipTransaction{
		AccountUserRepository: repositories.NewAccountUserRepository(nil),
	}
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
		if memberships.open {
			t.Fatal("mail was dispatched before the transaction committed")
		}
		stored, findErr := users.FindByID(context.Background(), userID)
		if findErr != nil || stored == nil || stored.Email != email {
			t.Fatal("mail was dispatched before the user was committed")
		}
		if stored.PasswordHash != onboardPasswordHash {
			t.Fatal("committed user is missing the password hash")
		}
		if count := rowCount(t, &models.Account{}, "name = ? OR name = ?", organization, organization+" [test]"); count != 2 {
			t.Fatalf("accounts visible to mail = %d", count)
		}
		if count := rowCount(t, &models.AccountUser{}, "user_id = ?", userID); count != 2 {
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
	if count := rowCount(t, &models.AccountUser{}, "user_id = ? AND role = ? AND status = ?", user.ID, "owner", models.MembershipStatusActive); count != 2 {
		t.Fatalf("owner memberships = %d", count)
	}
	if count := rowCount(t, &models.PlatformAdmin{}, "user_id = ?", user.ID); count != 0 {
		t.Fatalf("platform admin rows = %d", count)
	}
}

type membershipInsertFails struct {
	*repositories.AccountUserRepository
	userID    uuid.UUID
	accountID uuid.UUID
}

func (m *membershipInsertFails) Create(_ context.Context, row *models.AccountUser) error {
	if row == nil {
		return errors.New("membership insert failed")
	}
	m.userID = row.UserID
	m.accountID = row.AccountID
	return errors.New("membership insert failed")
}

type membershipTransaction struct {
	*repositories.AccountUserRepository
	open bool
}

func (m *membershipTransaction) Within(ctx context.Context, fn func(context.Context) error) error {
	m.open = true
	err := m.AccountUserRepository.Within(ctx, fn)
	m.open = false
	return err
}

func rowCount(t *testing.T, model any, query string, args ...any) int64 {
	t.Helper()
	count, err := facades.Orm().Query().Model(model).Where(query, args...).Count()
	if err != nil {
		t.Fatal(err)
	}
	return count
}
