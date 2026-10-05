package wallets

import (
	"testing"

	accountsvc "github.com/macrowallets/waas/app/services/account"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

func walletUsersControllerDeps() WalletUsersControllerDeps {
	return WalletUsersControllerDeps{
		Members:     &walletrecords.Members{},
		Accounts:    &accountsvc.Service{},
		Memberships: &walletrecords.Memberships{},
	}
}

func TestNewUsersControllerKeepsItsDependencies(t *testing.T) {
	deps := walletUsersControllerDeps()
	ctrl := NewUsersController(deps)
	if ctrl == nil {
		t.Fatal("NewUsersController returned nil")
	}
	if ctrl.members != deps.Members {
		t.Fatal("wallet users controller did not keep the wallet users service")
	}
	if ctrl.accounts != deps.Accounts {
		t.Fatal("wallet users controller did not keep the account service")
	}
	if ctrl.memberships != deps.Memberships {
		t.Fatal("wallet users controller did not keep the wallet memberships")
	}
}

func TestNewUsersControllerRequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*WalletUsersControllerDeps)
		panic string
	}{
		{
			name:  "wallet users service",
			clear: func(deps *WalletUsersControllerDeps) { deps.Members = nil },
			panic: "dashboard wallet users controller: wallet users service is required",
		},
		{
			name:  "account service",
			clear: func(deps *WalletUsersControllerDeps) { deps.Accounts = nil },
			panic: "dashboard wallet users controller: account service is required",
		},
		{
			name:  "wallet memberships",
			clear: func(deps *WalletUsersControllerDeps) { deps.Memberships = nil },
			panic: "dashboard wallet users controller: wallet memberships are required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := walletUsersControllerDeps()
			tc.clear(&deps)
			defer func() {
				got := recover()
				if got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewUsersController(deps)
			t.Fatal("expected a panic")
		})
	}
}
