package policies

import "testing"

func TestFund_Movement_FollowsAccountRole(t *testing.T) {
	moves := []string{FundWithdraw, FundSweep, FundCreateWallet}
	for _, role := range []string{"owner", "admin"} {
		for _, action := range append(moves, FundGenerateAddress) {
			if !MayPerformFundAction(role, action) {
				t.Fatalf("%s must be allowed to %s", role, action)
			}
		}
	}

	if !MayPerformFundAction("user", FundGenerateAddress) {
		t.Fatal("user must be allowed to generate an address")
	}
	for _, action := range moves {
		if MayPerformFundAction("user", action) {
			t.Fatalf("user must not %s", action)
		}
	}

	for _, role := range []string{"auditor", "viewer", "", "owner "} {
		for _, action := range append(moves, FundGenerateAddress) {
			if MayPerformFundAction(role, action) {
				t.Fatalf("%q must not %s", role, action)
			}
		}
	}
}
