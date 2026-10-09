package requests_test

import (
	"testing"

	addressesrequests "github.com/macrowallets/waas/app/http/requests/addresses"
	sweeprequests "github.com/macrowallets/waas/app/http/requests/sweep"
	walletsrequests "github.com/macrowallets/waas/app/http/requests/wallets"
)

func TestPassphrase_Forms_AcceptOnlyThePassphraseField(t *testing.T) {
	forms := []struct {
		name  string
		rules map[string]string
	}{
		{name: "create wallet", rules: (&walletsrequests.StoreRequest{}).Rules(nil)},
		{name: "generate address", rules: (&addressesrequests.StoreRequest{}).Rules(nil)},
		{name: "consolidate", rules: (&sweeprequests.ConsolidateRequest{}).Rules(nil)},
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
