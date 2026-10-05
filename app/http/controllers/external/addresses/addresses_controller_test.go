package addresses

import (
	"testing"

	chain "github.com/macrowallets/waas/app/services/chain"
	deposit "github.com/macrowallets/waas/app/services/deposit"
	wallet "github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

func addressesControllerDeps() AddressesControllerDeps {
	svc := &wallet.Service{}
	return AddressesControllerDeps{
		Addresses:     &walletrecords.Addresses{},
		WalletService: func() *wallet.Service { return svc },
		Deposits:      &deposit.Service{},
		Registry:      &chain.Registry{},
	}
}

func TestNewAddressesControllerKeepsItsDependencies(t *testing.T) {
	deps := addressesControllerDeps()
	ctrl := NewAddressesController(deps)
	if ctrl == nil {
		t.Fatal("NewAddressesController returned nil")
	}
	if ctrl.addresses != deps.Addresses {
		t.Fatal("addresses controller did not keep the addresses service")
	}
	if ctrl.walletService == nil || ctrl.walletService() != deps.WalletService() {
		t.Fatal("addresses controller did not keep the wallet service")
	}
	if ctrl.deposits != deps.Deposits {
		t.Fatal("addresses controller did not keep the deposit service")
	}
	if ctrl.registry != deps.Registry {
		t.Fatal("addresses controller did not keep the chain registry")
	}
}

func TestNewAddressesControllerRequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*AddressesControllerDeps)
		panic string
	}{
		{
			name:  "addresses service",
			clear: func(deps *AddressesControllerDeps) { deps.Addresses = nil },
			panic: "external addresses controller: addresses service is required",
		},
		{
			name:  "wallet service",
			clear: func(deps *AddressesControllerDeps) { deps.WalletService = nil },
			panic: "external addresses controller: wallet service is required",
		},
		{
			name: "wallet service result",
			clear: func(deps *AddressesControllerDeps) {
				deps.WalletService = func() *wallet.Service { return nil }
			},
			panic: "external addresses controller: wallet service is required",
		},
		{
			name:  "deposit service",
			clear: func(deps *AddressesControllerDeps) { deps.Deposits = nil },
			panic: "external addresses controller: deposit service is required",
		},
		{
			name:  "chain registry",
			clear: func(deps *AddressesControllerDeps) { deps.Registry = nil },
			panic: "external addresses controller: chain registry is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := addressesControllerDeps()
			tc.clear(&deps)
			defer func() {
				got := recover()
				if got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewAddressesController(deps)
			t.Fatal("expected a panic")
		})
	}
}
