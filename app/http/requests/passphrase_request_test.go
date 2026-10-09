package requests_test

import (
	"testing"

	"github.com/macrowallets/waas/app/http/requests"
	addressesrequests "github.com/macrowallets/waas/app/http/requests/addresses"
	sweeprequests "github.com/macrowallets/waas/app/http/requests/sweep"
)

func TestPassphrase_Forms_AcceptOnlyThePassphraseField(t *testing.T) {
	forms := []struct {
		name  string
		rules map[string]string
	}{
		{name: "create wallet", rules: (&requests.CreateWalletRequest{}).Rules(nil)},
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
