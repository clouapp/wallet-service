package account_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const onboardPasswordHash = "stored-password-hash"

func TestOnboard_Rolls_BackAccountAndMembershipWhenMembershipInsertFails(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	email := "onboard-rollback-" + uuid.NewString() + "@example.com"
	organization := "Onboard Rollback " + uuid.NewString()
	users := repositories.NewUserRepository(nil)
	accounts := repositories.NewAccountRepository(nil)
	memberships := repositories.NewAccountUserRepository(nil)
	refuseAccountUserInserts(t)
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
	if count := rowCount(t, &models.User{}, "email = ?", email); count != 0 {
		t.Fatalf("user rows = %d", count)
	}
	if count := rowCount(t, &models.Account{}, "name = ? OR name = ?", organization, organization+" [test]"); count != 0 {
		t.Fatalf("account rows = %d", count)
	}
	if count := rowCount(t, &models.AccountUser{}, "user_id IN (SELECT id FROM users WHERE email = ?)", email); count != 0 {
		t.Fatalf("membership rows = %d", count)
	}
	if count := rowCount(t, &models.PlatformAdmin{}, "user_id IN (SELECT id FROM users WHERE email = ?)", email); count != 0 {
		t.Fatalf("platform admin rows = %d", count)
	}
}

func TestOnboard_Commits_ThenDispatchesMail(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	email := "onboard-commit-" + uuid.NewString() + "@example.com"
	organization := "Onboard Commit " + uuid.NewString()
	users := repositories.NewUserRepository(nil)
	accounts := repositories.NewAccountRepository(nil)
	memberships := repositories.NewAccountUserRepository(nil)
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

// refuseAccountUserInserts makes the real membership repository's INSERT fail.
// The flag row is committed before Onboard, so the register transaction can
// roll back without clearing it. Cleanup removes the flag and the trigger.
func refuseAccountUserInserts(t *testing.T) {
	t.Helper()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS test_account_user_insert_fail (id int PRIMARY KEY)`,
		`CREATE OR REPLACE FUNCTION test_account_user_insert_fail() RETURNS trigger AS $fn$
BEGIN
	IF EXISTS (SELECT 1 FROM test_account_user_insert_fail) THEN
		RAISE EXCEPTION 'membership insert failed';
	END IF;
	RETURN NEW;
END;
$fn$ LANGUAGE plpgsql`,
		`DROP TRIGGER IF EXISTS test_account_user_insert_fail_trg ON account_users`,
		`CREATE TRIGGER test_account_user_insert_fail_trg
			BEFORE INSERT ON account_users
			FOR EACH ROW EXECUTE FUNCTION test_account_user_insert_fail()`,
		`DELETE FROM test_account_user_insert_fail`,
		`INSERT INTO test_account_user_insert_fail (id) VALUES (1)`,
	}
	for _, statement := range statements {
		if _, err := facades.Orm().Query().Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = facades.Orm().Query().Exec(`DELETE FROM test_account_user_insert_fail`)
		_, _ = facades.Orm().Query().Exec(`DROP TRIGGER IF EXISTS test_account_user_insert_fail_trg ON account_users`)
		_, _ = facades.Orm().Query().Exec(`DROP FUNCTION IF EXISTS test_account_user_insert_fail()`)
		_, _ = facades.Orm().Query().Exec(`DROP TABLE IF EXISTS test_account_user_insert_fail`)
	})
}

func rowCount(t *testing.T, model any, query string, args ...any) int64 {
	t.Helper()
	count, err := facades.Orm().Query().Model(model).Where(query, args...).Count()
	if err != nil {
		t.Fatal(err)
	}
	return count
}
