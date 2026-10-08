package seeds_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/database/seeds"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func TestAccount_Users_SeedDoesNotQueryOutsideTheRepository(t *testing.T) {
	source, err := os.ReadFile("account_users.go")
	if err != nil {
		t.Fatalf("read account users seed: %v", err)
	}
	text := string(source)
	for _, needle := range []string{"facades.", "Orm()", ".Query()", ".Exec(", ".Raw("} {
		if strings.Contains(text, needle) {
			t.Fatalf("account users seed still queries outside the repository (%s)", needle)
		}
	}
}

func TestSeed_Account_UsersInsertsMembershipsAndLeavesThemOnRerun(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()

	if err := seeds.SeedPairedAccounts(ctx); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	if err := seeds.SeedUsers(ctx); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if err := seeds.SeedAccountUsers(ctx); err != nil {
		t.Fatalf("seed account users: %v", err)
	}
	assertSeededAccountUsers(t, ctx)

	if err := seeds.SeedAccountUsers(ctx); err != nil {
		t.Fatalf("reseed account users: %v", err)
	}
	assertSeededAccountUsers(t, ctx)

	memberships := repositories.NewAccountUserRepository(nil)
	bobProdID := uuid.MustParse("00000000-0000-0000-0000-000000000032")
	if err := memberships.SetRole(ctx, bobProdID, "user"); err != nil {
		t.Fatalf("change membership role: %v", err)
	}
	if err := memberships.SetStatus(ctx, bobProdID, models.MembershipStatusSuspended); err != nil {
		t.Fatalf("change membership status: %v", err)
	}

	if err := seeds.SeedAccountUsers(ctx); err != nil {
		t.Fatalf("reseed after drift: %v", err)
	}

	bob, err := memberships.FindByID(ctx, bobProdID)
	if err != nil {
		t.Fatalf("find drifted membership: %v", err)
	}
	if bob.Role != "user" || bob.Status != models.MembershipStatusSuspended {
		t.Fatalf("reseed rewrote a membership that already existed: role=%q status=%q", bob.Role, bob.Status)
	}

	adminProd, err := memberships.FindByID(ctx, uuid.MustParse("00000000-0000-0000-0000-000000000030"))
	if err != nil {
		t.Fatalf("find unchanged membership: %v", err)
	}
	if adminProd.Role != "owner" || adminProd.Status != models.MembershipStatusActive {
		t.Fatalf("reseed changed another membership: role=%q status=%q", adminProd.Role, adminProd.Status)
	}
}

func assertSeededAccountUsers(t *testing.T, ctx context.Context) {
	t.Helper()
	prodID := uuid.MustParse("00000000-0000-0000-0000-000000000010")
	testID := uuid.MustParse("00000000-0000-0000-0000-000000000011")
	adminID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	aliceID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	bobID := uuid.MustParse("00000000-0000-0000-0000-000000000003")
	want := []struct {
		id        uuid.UUID
		accountID uuid.UUID
		userID    uuid.UUID
		role      string
	}{
		{uuid.MustParse("00000000-0000-0000-0000-000000000030"), prodID, adminID, "owner"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000031"), prodID, aliceID, "admin"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000032"), prodID, bobID, "auditor"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000033"), testID, adminID, "owner"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000034"), testID, aliceID, "admin"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000035"), testID, bobID, "auditor"},
	}
	memberships := repositories.NewAccountUserRepository(nil)
	for _, row := range want {
		found, err := memberships.FindByID(ctx, row.id)
		if err != nil {
			t.Fatalf("find membership %s: %v", row.id, err)
		}
		if found.AccountID != row.accountID || found.UserID != row.userID || found.Role != row.role || found.Status != models.MembershipStatusActive {
			t.Fatalf("membership %s has unexpected fields: account=%s user=%s role=%q status=%q", row.id, found.AccountID, found.UserID, found.Role, found.Status)
		}
	}
}
