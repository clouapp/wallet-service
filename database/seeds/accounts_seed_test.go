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
	"github.com/macrowallets/waas/tests/mocks"
)

func TestAccountsSeedDoesNotQueryOutsideTheRepository(t *testing.T) {
	source, err := os.ReadFile("accounts.go")
	if err != nil {
		t.Fatalf("read accounts seed: %v", err)
	}
	text := string(source)
	for _, needle := range []string{"facades.", "Orm()", ".Query()", ".Exec(", ".Raw("} {
		if strings.Contains(text, needle) {
			t.Fatalf("accounts seed still queries outside the repository (%s)", needle)
		}
	}
}

func TestSeedPairedAccountsInsertsAndRestoresTheLinkOnRerun(t *testing.T) {
	mocks.TestDB(t)
	ctx := context.Background()
	prodID := uuid.MustParse("00000000-0000-0000-0000-000000000010")
	testID := uuid.MustParse("00000000-0000-0000-0000-000000000011")
	accounts := repositories.NewAccountRepository(nil)

	if err := seeds.SeedPairedAccounts(ctx); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	assertPairedAccounts(t, ctx, accounts, prodID, testID)

	if err := seeds.SeedPairedAccounts(ctx); err != nil {
		t.Fatalf("reseed accounts: %v", err)
	}
	assertPairedAccounts(t, ctx, accounts, prodID, testID)

	otherID := uuid.MustParse("00000000-0000-0000-0000-000000000099")
	if err := accounts.Create(ctx, &models.Account{
		ID:          otherID,
		Name:        "Other",
		Status:      "active",
		Environment: models.EnvironmentProd,
	}); err != nil {
		t.Fatalf("create other account: %v", err)
	}
	if err := accounts.SetName(ctx, prodID, "Renamed Prod"); err != nil {
		t.Fatalf("rename prod: %v", err)
	}
	if err := accounts.SetStatus(ctx, prodID, "suspended"); err != nil {
		t.Fatalf("suspend prod: %v", err)
	}
	if err := accounts.SetViewAllWallets(ctx, prodID, false); err != nil {
		t.Fatalf("clear prod view all: %v", err)
	}
	if err := accounts.SetEnvironment(ctx, prodID, models.EnvironmentTest); err != nil {
		t.Fatalf("flip prod environment: %v", err)
	}
	if err := accounts.SetLinkedAccountID(ctx, prodID, otherID); err != nil {
		t.Fatalf("relink prod: %v", err)
	}
	if err := accounts.SetName(ctx, testID, "Renamed Test"); err != nil {
		t.Fatalf("rename test: %v", err)
	}
	if err := accounts.SetEnvironment(ctx, testID, models.EnvironmentProd); err != nil {
		t.Fatalf("flip test environment: %v", err)
	}
	if err := accounts.SetLinkedAccountID(ctx, testID, otherID); err != nil {
		t.Fatalf("relink test: %v", err)
	}

	if err := seeds.SeedPairedAccounts(ctx); err != nil {
		t.Fatalf("reseed after drift: %v", err)
	}

	prod, err := accounts.FindByID(ctx, prodID)
	if err != nil {
		t.Fatalf("find prod after drift: %v", err)
	}
	if prod.Name != "Renamed Prod" || prod.Status != "suspended" || prod.ViewAllWallets {
		t.Fatalf("reseed rewrote prod fields that already existed: name=%q status=%q view_all=%v", prod.Name, prod.Status, prod.ViewAllWallets)
	}
	if prod.Environment != models.EnvironmentProd || prod.LinkedAccountID == nil || *prod.LinkedAccountID != testID {
		t.Fatal("reseed did not restore the prod environment and link")
	}

	testAccount, err := accounts.FindByID(ctx, testID)
	if err != nil {
		t.Fatalf("find test after drift: %v", err)
	}
	if testAccount.Name != "Renamed Test" || testAccount.Status != "active" || !testAccount.ViewAllWallets {
		t.Fatalf("reseed rewrote test fields that already existed: name=%q status=%q view_all=%v", testAccount.Name, testAccount.Status, testAccount.ViewAllWallets)
	}
	if testAccount.Environment != models.EnvironmentTest || testAccount.LinkedAccountID == nil || *testAccount.LinkedAccountID != prodID {
		t.Fatal("reseed did not restore the test environment and link")
	}
}

func assertPairedAccounts(t *testing.T, ctx context.Context, accounts *repositories.AccountRepository, prodID, testID uuid.UUID) {
	t.Helper()
	prod, err := accounts.FindByID(ctx, prodID)
	if err != nil {
		t.Fatalf("find prod account: %v", err)
	}
	if prod.Name != "Acme Corp" || prod.Status != "active" || !prod.ViewAllWallets || prod.Environment != models.EnvironmentProd {
		t.Fatalf("prod account has unexpected fields: name=%q status=%q view_all=%v env=%q", prod.Name, prod.Status, prod.ViewAllWallets, prod.Environment)
	}
	if prod.LinkedAccountID == nil || *prod.LinkedAccountID != testID {
		t.Fatal("prod account is not linked to the test account")
	}

	testAccount, err := accounts.FindByID(ctx, testID)
	if err != nil {
		t.Fatalf("find test account: %v", err)
	}
	if testAccount.Name != "Acme Corp (Test)" || testAccount.Status != "active" || !testAccount.ViewAllWallets || testAccount.Environment != models.EnvironmentTest {
		t.Fatalf("test account has unexpected fields: name=%q status=%q view_all=%v env=%q", testAccount.Name, testAccount.Status, testAccount.ViewAllWallets, testAccount.Environment)
	}
	if testAccount.LinkedAccountID == nil || *testAccount.LinkedAccountID != prodID {
		t.Fatal("test account is not linked to the prod account")
	}
}
