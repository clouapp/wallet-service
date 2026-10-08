package policies

import "testing"

func TestGate_Ability_NamesStayTheLiveCatalog(t *testing.T) {
	t.Parallel()

	cases := []struct {
		got  string
		want string
	}{
		{AbilityAccountUpdateMember, "account.update-member"},
		{AbilityAccountViewSettings, "account.view-settings"},
		{AbilityAccountUpdateSettings, "account.update-settings"},
		{AbilityAccountReadActivity, "account.read-activity"},
		{AbilityAccountViewFeatures, "account.view-features"},
		{AbilityWalletArchive, "wallet.archive"},
		{AbilityWalletFreeze, "wallet.freeze"},
		{AbilityWalletAddUser, "wallet.add-user"},
		{AbilityWalletRemoveUser, "wallet.remove-user"},
		{AbilityWalletWhitelist, "wallet.whitelist"},
		{AbilityWalletManageWebhooks, "wallet.manage-webhooks"},
		{AbilityWalletCancelWithdrawal, "wallet.cancel-withdrawal"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Fatalf("gate ability %q is %q", tc.want, tc.got)
		}
	}
}
