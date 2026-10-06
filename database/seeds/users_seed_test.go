package seeds_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/database/seeds"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

func TestMain(m *testing.M) {
	testutil.BootTest()
	os.Exit(m.Run())
}

func TestUsers_Seed_DoesNotQueryOutsideTheRepository(t *testing.T) {
	source, err := os.ReadFile("users.go")
	if err != nil {
		t.Fatalf("read users seed: %v", err)
	}
	text := string(source)
	for _, needle := range []string{"facades.", "Orm()", ".Query()", ".Exec(", ".Raw("} {
		if strings.Contains(text, needle) {
			t.Fatalf("users seed still queries outside the repository (%s)", needle)
		}
	}
}

func TestSeed_Users_InsertsDashboardUsersAndKeepsThemOnRerun(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	acmeID := uuid.MustParse("00000000-0000-0000-0000-000000000010")
	accounts := repositories.NewAccountRepository(nil)
	if err := accounts.Create(ctx, &models.Account{
		ID:          acmeID,
		Name:        "Acme Corp",
		Status:      "active",
		Environment: models.EnvironmentProd,
	}); err != nil {
		t.Fatalf("create default account: %v", err)
	}

	if err := seeds.SeedUsers(ctx); err != nil {
		t.Fatalf("seed users: %v", err)
	}

	want := []struct {
		id       uuid.UUID
		email    string
		fullName string
	}{
		{uuid.MustParse("00000000-0000-0000-0000-000000000001"), "admin@macro.markets", "Admin User"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000002"), "alice@macro.markets", "Alice Smith"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000003"), "bob@macro.markets", "Bob Jones"},
	}
	users := repositories.NewUserRepository(nil)
	hashes := make([]string, len(want))
	for i, row := range want {
		found, err := users.FindByID(ctx, row.id)
		if err != nil {
			t.Fatalf("find seeded user %s: %v", row.email, err)
		}
		if found.Email != row.email || found.FullName != row.fullName || found.Status != "active" || found.TotpEnabled {
			t.Fatalf("seeded user %s has unexpected profile fields", row.email)
		}
		if found.DefaultAccountID == nil || *found.DefaultAccountID != acmeID {
			t.Fatalf("seeded user %s is missing the default account", row.email)
		}
		if found.Preferences == nil || found.Preferences.PreferredFiatCode != "" || found.Preferences.DisplayInFiat != nil {
			t.Fatalf("seeded user %s preferences are not the empty document", row.email)
		}
		if err := bcrypt.CompareHashAndPassword([]byte(found.PasswordHash), []byte("secret")); err != nil {
			t.Fatal("seed password does not match the stored hash")
		}
		hashes[i] = found.PasswordHash
	}

	if err := seeds.SeedUsers(ctx); err != nil {
		t.Fatalf("reseed users: %v", err)
	}
	for i, row := range want {
		found, err := users.FindByID(ctx, row.id)
		if err != nil {
			t.Fatalf("find reseeded user %s: %v", row.email, err)
		}
		if found.PasswordHash != hashes[i] {
			t.Fatal("reseed replaced the password hash")
		}
		if found.FullName != row.fullName {
			t.Fatalf("reseed changed the name of %s", row.email)
		}
	}

	otherID := uuid.MustParse("00000000-0000-0000-0000-000000000099")
	if err := accounts.Create(ctx, &models.Account{
		ID:          otherID,
		Name:        "Other",
		Status:      "active",
		Environment: models.EnvironmentProd,
	}); err != nil {
		t.Fatalf("create other account: %v", err)
	}
	adminID := want[0].id
	if err := users.UpdateFullName(ctx, adminID, "Renamed Admin"); err != nil {
		t.Fatalf("rename admin: %v", err)
	}
	if err := users.UpdateDefaultAccountID(ctx, adminID, &otherID); err != nil {
		t.Fatalf("move default account: %v", err)
	}
	aliceID := want[1].id
	if err := users.UpdateDefaultAccountID(ctx, aliceID, nil); err != nil {
		t.Fatalf("clear default account: %v", err)
	}

	if err := seeds.SeedUsers(ctx); err != nil {
		t.Fatalf("reseed after drift: %v", err)
	}
	admin, err := users.FindByID(ctx, adminID)
	if err != nil {
		t.Fatalf("find admin after drift: %v", err)
	}
	if admin.DefaultAccountID == nil || *admin.DefaultAccountID != acmeID {
		t.Fatal("reseed did not restore the admin default account")
	}
	if admin.FullName != "Renamed Admin" {
		t.Fatal("reseed rewrote a name that already existed")
	}
	if admin.PasswordHash != hashes[0] {
		t.Fatal("reseed replaced the password hash")
	}
	alice, err := users.FindByID(ctx, aliceID)
	if err != nil {
		t.Fatalf("find alice after drift: %v", err)
	}
	if alice.DefaultAccountID == nil || *alice.DefaultAccountID != acmeID {
		t.Fatal("reseed did not restore a cleared default account")
	}
}
