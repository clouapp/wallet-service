package policies

import "testing"

func TestSees_Every_AccountWalletFollowsTheFlagForUserAndAuditor(t *testing.T) {
	for _, role := range []string{"owner", "admin"} {
		if !SeesEveryAccountWallet(role, false) || !SeesEveryAccountWallet(role, true) {
			t.Fatalf("%s must see every wallet whether or not the flag is set", role)
		}
	}

	for _, role := range []string{"user", "auditor", "viewer"} {
		if SeesEveryAccountWallet(role, false) {
			t.Fatalf("%s must not see every wallet when view_all_wallets is false", role)
		}
		if !SeesEveryAccountWallet(role, true) {
			t.Fatalf("%s must see every wallet when view_all_wallets is true", role)
		}
	}

	for _, role := range []string{"", "owner ", "spender"} {
		if SeesEveryAccountWallet(role, true) || SeesEveryAccountWallet(role, false) {
			t.Fatalf("%q must not see every wallet", role)
		}
	}
}
