package policies

import (
	"slices"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

func TestCanRolesReadIsOwnerAdminAndAuditor(t *testing.T) {
	for _, role := range []string{models.AccountRoleOwner, models.AccountRoleAdmin, models.AccountRoleAuditor, models.RetiredAccountRoleViewer} {
		if !Can(AccountRoleGrants(role), PermRolesRead) {
			t.Fatalf("%s must hold roles.read", role)
		}
	}
	for _, role := range []string{models.AccountRoleUser, "", "spender", "owner "} {
		if Can(AccountRoleGrants(role), PermRolesRead) {
			t.Fatalf("%q must not hold roles.read", role)
		}
	}
}

func TestEffectiveRoleGrantsFollowTheLiveGates(t *testing.T) {
	grants := EffectiveRoleGrants()
	if len(grants) != len(models.AccountRoles()) {
		t.Fatalf("roles = %d, want %d", len(grants), len(models.AccountRoles()))
	}
	for i, role := range models.AccountRoles() {
		if grants[i].Role != role {
			t.Fatalf("role[%d] = %q, want %q", i, grants[i].Role, role)
		}
		if !slices.IsSorted(grants[i].Permissions) {
			t.Fatalf("%s permissions are not sorted: %v", role, grants[i].Permissions)
		}
		if slices.Contains(grants[i].Permissions, "roles.write") {
			t.Fatalf("%s holds roles.write", role)
		}
	}

	owner := permissionsOf(t, grants, models.AccountRoleOwner)
	for _, permission := range []string{
		PermRolesRead, PermUsersRead, PermUsersWrite, PermSettingsRead, PermSettingsWrite,
		PermActivityRead, PermTokensRead, PermTokensWrite, PermAddressesCreate, PermAccountWrite,
		PermWithdrawalsCreate, PermSweepExecute, PermWalletsCreate, PermWalletsRead, PermWebhooksWrite,
	} {
		if !slices.Contains(owner, permission) {
			t.Fatalf("owner missing %s in %v", permission, owner)
		}
	}

	admin := permissionsOf(t, grants, models.AccountRoleAdmin)
	if !slices.Equal(admin, owner) {
		t.Fatalf("admin = %v, want the owner set %v", admin, owner)
	}

	user := permissionsOf(t, grants, models.AccountRoleUser)
	if !slices.Equal(user, []string{PermAddressesCreate}) {
		t.Fatalf("user = %v, want [%s]", user, PermAddressesCreate)
	}

	auditor := permissionsOf(t, grants, models.AccountRoleAuditor)
	for _, permission := range []string{PermRolesRead, PermUsersRead, PermSettingsRead, PermActivityRead, PermTokensRead} {
		if !slices.Contains(auditor, permission) {
			t.Fatalf("auditor missing %s in %v", permission, auditor)
		}
	}
	for _, permission := range []string{PermUsersWrite, PermSettingsWrite, PermTokensWrite, PermAddressesCreate, PermAccountWrite, PermWithdrawalsCreate, PermSweepExecute, PermWalletsCreate, PermWalletsRead, PermWebhooksWrite} {
		if slices.Contains(auditor, permission) {
			t.Fatalf("auditor holds %s", permission)
		}
	}
}

func TestAccountPermissionCatalogIsTheLiveCodeCatalog(t *testing.T) {
	catalog := AccountPermissionCatalog()
	if catalog == nil {
		t.Fatal("catalog is nil")
	}
	if !slices.IsSorted(catalog) {
		t.Fatalf("catalog is not sorted: %v", catalog)
	}
	seen := map[string]struct{}{}
	for _, permission := range catalog {
		if permission == "" {
			t.Fatal("catalog contains an empty permission")
		}
		if _, ok := seen[permission]; ok {
			t.Fatalf("duplicate permission %s", permission)
		}
		seen[permission] = struct{}{}
	}
	if slices.Contains(catalog, "roles.write") {
		t.Fatal("catalog contains roles.write")
	}

	union := map[string]struct{}{}
	for _, grant := range EffectiveRoleGrants() {
		for _, permission := range grant.Permissions {
			union[permission] = struct{}{}
		}
	}
	if len(union) != len(catalog) {
		t.Fatalf("catalog has %d permissions, role grants have %d", len(catalog), len(union))
	}
	for permission := range union {
		if !slices.Contains(catalog, permission) {
			t.Fatalf("catalog missing %s", permission)
		}
	}
	for _, permission := range []string{
		PermRolesRead, PermAddressesCreate, PermWithdrawalsCreate, PermSweepExecute, PermWalletsCreate,
	} {
		if !slices.Contains(catalog, permission) {
			t.Fatalf("catalog missing %s in %v", permission, catalog)
		}
	}
}

func permissionsOf(t *testing.T, grants []RoleGrant, role string) []string {
	t.Helper()
	for _, grant := range grants {
		if grant.Role == role {
			return grant.Permissions
		}
	}
	t.Fatalf("missing role %s", role)
	return nil
}
