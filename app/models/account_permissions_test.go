package models

import "testing"

func TestAccountPermissionsAreTheClosedCatalog(t *testing.T) {
	want := []string{
		"account.read",
		"account.write",
		"account.lifecycle",
		"users.read",
		"users.write",
		"roles.read",
		"roles.write",
		"tokens.read",
		"tokens.write",
		"settings.read",
		"settings.write",
		"activity.read",
		"wallets.read",
		"wallets.create",
		"wallets.write",
		"wallet_users.write",
		"addresses.create",
		"whitelist.write",
		"webhooks.read",
		"webhooks.write",
		"withdrawals.create",
		"withdrawals.approve",
		"withdrawals.cancel",
		"sweep.execute",
	}
	got := AccountPermissions()
	if len(got) != len(want) {
		t.Fatalf("permissions = %v, want %v", got, want)
	}
	seen := map[string]struct{}{}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("permissions[%d] = %q, want %q", i, got[i], want[i])
		}
		if _, ok := seen[got[i]]; ok {
			t.Fatalf("duplicate permission %s", got[i])
		}
		seen[got[i]] = struct{}{}
		if !IsAccountPermission(got[i]) {
			t.Fatalf("%s is missing from IsAccountPermission", got[i])
		}
	}
	for _, name := range []string{"", "transactions.read", "viewer", "roles.write "} {
		if IsAccountPermission(name) {
			t.Fatalf("%q is not an account permission", name)
		}
	}
}
