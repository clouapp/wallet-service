package requests

import "testing"

func TestPassphrase_Forms_AcceptOnlyThePassphraseField(t *testing.T) {
	forms := []struct {
		name  string
		rules map[string]string
	}{
		{name: "create wallet", rules: (&CreateWalletRequest{}).Rules(nil)},
		{name: "create wallet admin", rules: (&CreateWalletAdminRequest{}).Rules(nil)},
		{name: "generate address", rules: (&GenerateAddressRequest{}).Rules(nil)},
		{name: "withdraw", rules: (&CreateWalletWithdrawalRequest{}).Rules(nil)},
		{name: "consolidate", rules: (&ConsolidateRequest{}).Rules(nil)},
	}
	for _, form := range forms {
		t.Run(form.name, func(t *testing.T) {
			if _, ok := form.rules["passphrase"]; !ok {
				t.Fatal("passphrase is not a form field")
			}
			for _, alias := range []string{"confirm_passphrase", "password", "wallet_password"} {
				if _, ok := form.rules[alias]; ok {
					t.Fatalf("form still reads %s", alias)
				}
			}
		})
	}
}
