package wallets

import (
	"testing"

	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

func walletSettingsControllerDeps() WalletSettingsControllerDeps {
	return WalletSettingsControllerDeps{
		Wallets:     &walletrecords.Wallets{},
		Memberships: &walletrecords.Memberships{},
		Chains:      &chainsvc.Service{},
	}
}

func TestNewSettingsControllerKeepsItsDependencies(t *testing.T) {
	deps := walletSettingsControllerDeps()
	ctrl := NewSettingsController(deps)
	if ctrl == nil {
		t.Fatal("NewSettingsController returned nil")
	}
	if ctrl.wallets != deps.Wallets {
		t.Fatal("wallet settings controller did not keep the wallets service")
	}
	if ctrl.memberships != deps.Memberships {
		t.Fatal("wallet settings controller did not keep the wallet memberships")
	}
	if ctrl.chains != deps.Chains {
		t.Fatal("wallet settings controller did not keep the chains service")
	}
}

func TestNewSettingsControllerRequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*WalletSettingsControllerDeps)
		panic string
	}{
		{
			name:  "wallets service",
			clear: func(deps *WalletSettingsControllerDeps) { deps.Wallets = nil },
			panic: "dashboard wallet settings controller: wallets service is required",
		},
		{
			name:  "wallet memberships",
			clear: func(deps *WalletSettingsControllerDeps) { deps.Memberships = nil },
			panic: "dashboard wallet settings controller: wallet memberships are required",
		},
		{
			name:  "chains service",
			clear: func(deps *WalletSettingsControllerDeps) { deps.Chains = nil },
			panic: "dashboard wallet settings controller: chains service is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := walletSettingsControllerDeps()
			tc.clear(&deps)
			defer func() {
				got := recover()
				if got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewSettingsController(deps)
			t.Fatal("expected a panic")
		})
	}
}
