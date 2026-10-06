package policies

import (
	"slices"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

func TestWallet_Role_CatalogNamesEachRoleGrant(t *testing.T) {
	catalog := WalletRoleCatalog()
	if len(catalog) != 4 {
		t.Fatalf("roles = %d, want 4", len(catalog))
	}

	want := []struct {
		role        string
		permissions []string
	}{
		{models.WalletRoleAdmin, []string{
			models.AccountPermWalletUsersWrite,
			models.AccountPermWalletsWrite,
			models.AccountPermWebhooksWrite,
			models.AccountPermWhitelistWrite,
		}},
		{models.WalletRoleSpender, []string{
			models.AccountPermAddressesCreate,
			models.AccountPermSweepExecute,
			models.AccountPermWithdrawalsCreate,
		}},
		{models.WalletRoleApprover, []string{models.AccountPermWithdrawalsApprove}},
		{models.WalletRoleViewer, []string{models.AccountPermWalletsRead}},
	}
	for i, entry := range want {
		got := catalog[i]
		if got.Role != entry.role {
			t.Fatalf("role[%d] = %q, want %q", i, got.Role, entry.role)
		}
		if !slices.Equal(got.Permissions, entry.permissions) {
			t.Fatalf("%s = %v, want %v", got.Role, got.Permissions, entry.permissions)
		}
		if !slices.IsSorted(got.Permissions) {
			t.Fatalf("%s permissions are not sorted: %v", got.Role, got.Permissions)
		}
		if slices.Contains(got.Permissions, models.AccountPermRolesWrite) {
			t.Fatalf("%s holds roles.write", got.Role)
		}
	}

	for _, accountRole := range AccountRoleCatalog() {
		if accountRole.Role == models.WalletRoleViewer {
			t.Fatal("viewer is an account role")
		}
		if slices.Contains(accountRole.Permissions, models.AccountPermWithdrawalsApprove) {
			t.Fatalf("%s holds withdrawals.approve", accountRole.Role)
		}
	}

	user := rolePermissions(models.AccountRoleUser)
	for _, permission := range []string{
		models.AccountPermWithdrawalsCreate,
		models.AccountPermSweepExecute,
		models.AccountPermWalletsCreate,
	} {
		if slices.Contains(user, permission) {
			t.Fatalf("user holds %s", permission)
		}
	}

	for _, role := range []string{models.AccountRoleOwner, models.AccountRoleAdmin, models.AccountRoleUser} {
		grants := WalletGrants(role)
		if len(grants) != 1 {
			t.Fatalf("%s WalletGrants = %d, want 1", role, len(grants))
		}
		if _, ok := grants[models.AccountPermAddressesCreate]; !ok {
			t.Fatalf("%s WalletGrants does not hold addresses.create", role)
		}
		if _, ok := grants[models.AccountPermWithdrawalsCreate]; ok {
			t.Fatalf("%s WalletGrants holds withdrawals.create", role)
		}
		if _, ok := grants[models.AccountPermSweepExecute]; ok {
			t.Fatalf("%s WalletGrants holds sweep.execute", role)
		}
		if _, ok := grants[models.AccountPermWalletsCreate]; ok {
			t.Fatalf("%s WalletGrants holds wallets.create", role)
		}
	}
	if grants := WalletGrants(models.AccountRoleAuditor); len(grants) != 0 {
		t.Fatalf("auditor WalletGrants = %d, want 0", len(grants))
	}
	if grants := WalletGrants(models.WalletRoleViewer); len(grants) != 0 {
		t.Fatalf("wallet viewer WalletGrants = %d, want 0", len(grants))
	}
}
