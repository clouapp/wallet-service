package policies

import "testing"

func TestGate_Ability_NamesStayTheLiveCatalog(t *testing.T) {
	t.Parallel()

	cases := []struct {
		got  string
		want string
	}{
		{AbilityAccountView, "account.view"},
		{AbilityAccountUpdate, "account.update"},
		{AbilityAccountDelete, "account.delete"},
		{AbilityAccountAddUser, "account.add-user"},
		{AbilityAccountRemoveUser, "account.remove-user"},
		{AbilityAccountFreeze, "account.freeze"},
		{AbilityAccountArchive, "account.archive"},
		{AbilityWalletView, "wallet.view"},
		{AbilityWalletUpdate, "wallet.update"},
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
