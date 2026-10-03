package policies

import "testing"

func TestAPITokenPermissionCatalogIsClosed(t *testing.T) {
	t.Parallel()

	want := []string{
		"wallets.read",
		"wallets.create",
		"addresses.create",
		"withdrawals.create",
		"sweep.execute",
		"webhooks.read",
		"webhooks.write",
		"transactions.read",
	}
	got := APITokenPermissionCatalog()
	if len(got) != len(want) {
		t.Fatalf("catalog = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("catalog[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if IsAPITokenPermission("wallets:read") || IsAPITokenPermission("wallet.view") {
		t.Fatal("catalog accepted a name the plan does not list")
	}
}

func TestHoldsAPITokenPermission(t *testing.T) {
	t.Parallel()

	catalog := APITokenPermissionCatalog()
	for _, permission := range catalog {
		if !HoldsAPITokenPermission(roleOwner, permission) || !HoldsAPITokenPermission(roleAdmin, permission) {
			t.Fatalf("owner and admin hold %s", permission)
		}
	}

	userHolds := map[string]bool{
		PermWalletsRead:       true,
		PermAddressesCreate:   true,
		PermWithdrawalsCreate: true,
		PermSweepExecute:      true,
		PermWebhooksRead:      true,
	}
	auditorHolds := map[string]bool{
		PermWalletsRead:      true,
		PermWebhooksRead:     true,
		PermTransactionsRead: true,
	}
	for _, permission := range catalog {
		if HoldsAPITokenPermission(roleUser, permission) != userHolds[permission] {
			t.Fatalf("user hold %s = %v", permission, HoldsAPITokenPermission(roleUser, permission))
		}
		if HoldsAPITokenPermission(roleAuditor, permission) != auditorHolds[permission] {
			t.Fatalf("auditor hold %s = %v", permission, HoldsAPITokenPermission(roleAuditor, permission))
		}
		if HoldsAPITokenPermission("viewer", permission) || HoldsAPITokenPermission("", permission) {
			t.Fatalf("unknown role holds %s", permission)
		}
	}
	if HoldsAPITokenPermission(roleOwner, "wallets:read") {
		t.Fatal("owner holds a name outside the catalog")
	}
}

func TestMintAPITokenPermissionsRefusesWhatTheCreatorDoesNotHold(t *testing.T) {
	t.Parallel()

	if !MintAPITokenPermissions(roleOwner, nil).Allowed() {
		t.Fatal("an empty grant is allowed")
	}
	if !MintAPITokenPermissions(roleOwner, []string{}).Allowed() {
		t.Fatal("an empty list is allowed")
	}
	if !MintAPITokenPermissions(roleAdmin, []string{PermWalletsCreate, PermWebhooksWrite}).Allowed() {
		t.Fatal("admin holds the catalog")
	}
	if !MintAPITokenPermissions(roleUser, []string{PermWalletsRead, PermSweepExecute}).Allowed() {
		t.Fatal("user holds the operate set")
	}

	denied := MintAPITokenPermissions(roleUser, []string{PermWalletsRead, PermWalletsCreate})
	if denied.Allowed() {
		t.Fatal("user must not mint wallets.create")
	}
	if denied.Message() != apiTokenPermissionNotHeld {
		t.Fatalf("message = %q", denied.Message())
	}
	if MintAPITokenPermissions(roleAuditor, []string{PermTransactionsRead, PermSweepExecute}).Allowed() {
		t.Fatal("auditor must not mint sweep.execute")
	}
	if MintAPITokenPermissions(roleOwner, []string{"wallets:read"}).Allowed() {
		t.Fatal("a name outside the catalog is not held")
	}
}
