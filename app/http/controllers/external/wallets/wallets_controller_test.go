package wallets

import (
	"testing"

	wallet "github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

func walletsControllerDeps() (WalletsControllerDeps, *wallet.Service) {
	svc := &wallet.Service{}
	return WalletsControllerDeps{
		Wallets:       &walletrecords.Wallets{},
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
			panic: "external wallets controller: wallets service is required",
		},
		{
			name:  "wallet service",
			clear: func(deps *WalletsControllerDeps) { deps.WalletService = nil },
			panic: "external wallets controller: wallet service is required",
		},
		{
			name: "wallet service result",
			clear: func(deps *WalletsControllerDeps) {
				deps.WalletService = func() *wallet.Service { return nil }
			},
			panic: "external wallets controller: wallet service is required",
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
