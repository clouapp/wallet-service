package models

import "testing"

func TestParseWalletRolesAcceptsOnlyTheClosedSet(t *testing.T) {
	got, err := ParseWalletRoles("viewer, spender")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != WalletRoleViewer || got[1] != WalletRoleSpender {
		t.Fatalf("parsed = %v", got)
	}
	for _, raw := range []string{"", "owner", "auditor", "admin,nope", "viewer,viewer"} {
		if _, err := ParseWalletRoles(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	for _, raw := range []string{"admin", "spender", "approver", "viewer"} {
		if _, err := ParseWalletRoles(raw); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
	}
}
