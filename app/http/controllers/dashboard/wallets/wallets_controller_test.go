package wallets

import (
	"testing"

	chainsvc "github.com/macrowallets/waas/app/services/chains"
	wallet "github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

func walletsControllerDeps() (WalletsControllerDeps, *wallet.Service) {
	svc := &wallet.Service{}
	return WalletsControllerDeps{
		Wallets:       &walletrecords.Wallets{},
		Members:       &walletrecords.Members{},
		Balances:      &walletrecords.Balances{},
		Chains:        &chainsvc.Service{},
		WalletService: func() *wallet.Service { return svc },
	}, svc
}

func TestNew_Wallets_ControllerKeepsItsDependencies(t *testing.T) {
	deps, svc := walletsControllerDeps()
	ctrl := NewWalletsController(deps)
	if ctrl == nil {
		t.Fatal("NewWalletsController returned nil")
	}
	if ctrl.wallets != deps.Wallets {
		t.Fatal("wallets controller did not keep the wallets service")
	}
	if ctrl.members != deps.Members {
		t.Fatal("wallets controller did not keep the wallet members service")
	}
	if ctrl.balances != deps.Balances {
		t.Fatal("wallets controller did not keep the balances service")
	}
	if ctrl.chains != deps.Chains {
		t.Fatal("wallets controller did not keep the chains service")
	}
	if ctrl.walletService == nil || ctrl.walletService() != svc {
		t.Fatal("wallets controller did not keep the wallet service")
	}
}

func TestNew_Wallets_ControllerRequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*WalletsControllerDeps)
		panic string
	}{
		{
			name:  "wallets service",
			clear: func(deps *WalletsControllerDeps) { deps.Wallets = nil },
			panic: "dashboard wallets controller: wallets service is required",
		},
		{
			name:  "wallet members service",
			clear: func(deps *WalletsControllerDeps) { deps.Members = nil },
			panic: "dashboard wallets controller: wallet members service is required",
		},
		{
			name:  "balances service",
			clear: func(deps *WalletsControllerDeps) { deps.Balances = nil },
			panic: "dashboard wallets controller: balances service is required",
		},
		{
			name:  "chains service",
			clear: func(deps *WalletsControllerDeps) { deps.Chains = nil },
			panic: "dashboard wallets controller: chains service is required",
		},
		{
			name:  "wallet service",
			clear: func(deps *WalletsControllerDeps) { deps.WalletService = nil },
			panic: "dashboard wallets controller: wallet service is required",
		},
		{
			name: "wallet service result",
			clear: func(deps *WalletsControllerDeps) {
				deps.WalletService = func() *wallet.Service { return nil }
			},
			panic: "dashboard wallets controller: wallet service is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, _ := walletsControllerDeps()
			tc.clear(&deps)
			defer func() {
				got := recover()
				if got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewWalletsController(deps)
			t.Fatal("expected a panic")
		})
	}
}
