package policies

import (
	"slices"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

func TestAccountRoleCatalogRanksAndLiveGrants(t *testing.T) {
	catalog := AccountRoleCatalog()
	if len(catalog) != 4 {
		t.Fatalf("roles = %d, want 4", len(catalog))
	}

	wantRanks := []struct {
		role string
		rank int
	}{
		{models.AccountRoleOwner, 3},
		{models.AccountRoleAdmin, 2},
		{models.AccountRoleAuditor, 1},
		{models.AccountRoleUser, 1},
	}
	for i, want := range wantRanks {
		got := catalog[i]
		if got.Role != want.role {
			t.Fatalf("role[%d] = %q, want %q", i, got.Role, want.role)
		}
		if got.Rank != want.rank {
			t.Fatalf("%s rank = %d, want %d", got.Role, got.Rank, want.rank)
		}
		rank, ok := AccountRoleRank(got.Role)
		if !ok || rank != got.Rank {
			t.Fatalf("%s catalog rank %d, AccountRoleRank %d ok=%v", got.Role, got.Rank, rank, ok)
		}
		if got.Role == models.RetiredAccountRoleViewer {
			t.Fatal("viewer is not an account role")
		}
	}
	if catalog[0].Rank <= catalog[1].Rank || catalog[1].Rank <= catalog[2].Rank || catalog[2].Rank != catalog[3].Rank {
		t.Fatalf("rank ladder = owner %d admin %d auditor %d user %d", catalog[0].Rank, catalog[1].Rank, catalog[2].Rank, catalog[3].Rank)
	}

	for _, entry := range catalog {
		if slices.Contains(entry.Permissions, models.AccountPermRolesWrite) {
			t.Fatalf("%s holds roles.write", entry.Role)
		}
		if !slices.IsSorted(entry.Permissions) {
			t.Fatalf("%s permissions are not sorted: %v", entry.Role, entry.Permissions)
		}
	}
	if !slices.Contains(models.AccountPermissions(), models.AccountPermRolesWrite) {
		t.Fatal("roles.write left the closed catalog")
	}

	owner := catalogPermissionsOf(t, catalog, models.AccountRoleOwner)
	wantOwner := []string{
		models.AccountPermAccountLifecycle,
		models.AccountPermAccountRead,
		models.AccountPermAccountWrite,
		models.AccountPermActivityRead,
		models.AccountPermAddressesCreate,
		models.AccountPermRolesRead,
		models.AccountPermSettingsRead,
		models.AccountPermSettingsWrite,
		models.AccountPermSweepExecute,
		models.AccountPermTokensRead,
		models.AccountPermTokensWrite,
		models.AccountPermUsersRead,
		models.AccountPermUsersWrite,
		models.AccountPermWalletUsersWrite,
		models.AccountPermWalletsCreate,
		models.AccountPermWalletsRead,
		models.AccountPermWalletsWrite,
		models.AccountPermWebhooksRead,
		models.AccountPermWebhooksWrite,
		models.AccountPermWhitelistWrite,
		models.AccountPermWithdrawalsCancel,
		models.AccountPermWithdrawalsCreate,
	}
	if !slices.Equal(owner, wantOwner) {
		t.Fatalf("owner = %v, want %v", owner, wantOwner)
	}
	if slices.Contains(owner, models.AccountPermWithdrawalsApprove) {
		t.Fatal("owner holds withdrawals.approve")
	}
	if !ownerGatesAllow(models.AccountPermAccountRead) || !ownerGatesAllow(models.AccountPermAccountWrite) ||
		!ownerGatesAllow(models.AccountPermAccountLifecycle) || !ownerGatesAllow(models.AccountPermWalletsWrite) ||
		!ownerGatesAllow(models.AccountPermWalletUsersWrite) || !ownerGatesAllow(models.AccountPermWhitelistWrite) ||
		!ownerGatesAllow(models.AccountPermWebhooksRead) || !ownerGatesAllow(models.AccountPermWithdrawalsCancel) {
		t.Fatal("owner catalog includes a name the live gates refuse")
	}
	if ownerGatesAllow(models.AccountPermRolesWrite) || ownerGatesAllow(models.AccountPermWithdrawalsApprove) {
		t.Fatal("roles.write or withdrawals.approve became an owner gate")
	}

	admin := catalogPermissionsOf(t, catalog, models.AccountRoleAdmin)
	if !slices.Equal(admin, rolePermissions(models.AccountRoleAdmin)) {
		t.Fatalf("admin = %v, want live grant %v", admin, rolePermissions(models.AccountRoleAdmin))
	}
	if slices.Contains(admin, models.AccountPermAccountLifecycle) {
		t.Fatal("admin holds account.lifecycle")
	}

	user := catalogPermissionsOf(t, catalog, models.AccountRoleUser)
	if !slices.Equal(user, rolePermissions(models.AccountRoleUser)) {
		t.Fatalf("user = %v, want live grant %v", user, rolePermissions(models.AccountRoleUser))
	}
	for _, permission := range []string{models.AccountPermWithdrawalsCreate, models.AccountPermSweepExecute, models.AccountPermWalletsCreate} {
		if slices.Contains(user, permission) {
			t.Fatalf("user holds %s", permission)
		}
	}

	auditor := catalogPermissionsOf(t, catalog, models.AccountRoleAuditor)
	if !slices.Equal(auditor, rolePermissions(models.AccountRoleAuditor)) {
		t.Fatalf("auditor = %v, want live grant %v", auditor, rolePermissions(models.AccountRoleAuditor))
	}
	for _, permission := range auditor {
		if !strings.HasSuffix(permission, ".read") {
			t.Fatalf("auditor holds write grant %s", permission)
		}
	}
}

func catalogPermissionsOf(t *testing.T, catalog []AccountRole, role string) []string {
	t.Helper()
	for _, entry := range catalog {
		if entry.Role == role {
			return entry.Permissions
		}
	}
	t.Fatalf("missing role %s", role)
	return nil
}

func ownerGatesAllow(permission string) bool {
	for _, name := range ownerPermissionsAlreadyAllowed() {
		if name == permission {
			return true
		}
	}
	for _, name := range rolePermissions(roleOwner) {
		if name == permission {
			return true
		}
	}
	return false
}
