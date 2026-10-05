package wallets

import (
	"testing"

	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

func balancesControllerDeps() BalancesControllerDeps {
	return BalancesControllerDeps{
		Balances: &walletrecords.Balances{},
		Tokens:   &chainsvc.Service{},
	}
}

func TestNewBalancesControllerKeepsItsDependencies(t *testing.T) {
	deps := balancesControllerDeps()
	ctrl := NewBalancesController(deps)
	if ctrl == nil {
		t.Fatal("NewBalancesController returned nil")
	}
	if ctrl.balances != deps.Balances {
		t.Fatal("balances controller did not keep the balances service")
	}
	if ctrl.tokens != deps.Tokens {
		t.Fatal("balances controller did not keep the chains service")
	}
}

func TestNewBalancesControllerRequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*BalancesControllerDeps)
		panic string
	}{
		{
			name:  "balances service",
			clear: func(deps *BalancesControllerDeps) { deps.Balances = nil },
			panic: "dashboard balances controller: balances service is required",
		},
		{
			name:  "chains service",
			clear: func(deps *BalancesControllerDeps) { deps.Tokens = nil },
			panic: "dashboard balances controller: chains service is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := balancesControllerDeps()
			tc.clear(&deps)
			defer func() {
				got := recover()
				if got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewBalancesController(deps)
			t.Fatal("expected a panic")
		})
	}
}
