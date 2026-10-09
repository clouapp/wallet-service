package wallets

import (
	"testing"

	"github.com/macrowallets/waas/app/services/walletops"
	"github.com/macrowallets/waas/app/services/walletview"
)

func TestNew_WalletController_RequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		view  *walletview.Service
		ops   *walletops.Service
		panic string
	}{
		{"wallet view", nil, &walletops.Service{}, "external wallets controller: wallet view is required"},
		{"wallet operations", &walletview.Service{}, nil, "external wallets controller: wallet operations are required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewWalletController(tc.view, tc.ops)
			t.Fatal("expected a panic")
		})
	}
}

func TestNew_WalletController_KeepsItsDependencies(t *testing.T) {
	view, ops := &walletview.Service{}, &walletops.Service{}

	ctrl := NewWalletController(view, ops)

	if ctrl.view != view || ctrl.ops != ops {
		t.Fatal("wallet controller did not keep its dependencies")
	}
}
