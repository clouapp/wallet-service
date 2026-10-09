package balances

import (
	"testing"

	"github.com/macrowallets/waas/app/services/walletview"
)

func TestNew_BalanceController_RequiresTheWalletView(t *testing.T) {
	const want = "dashboard balances controller: wallet view is required"
	defer func() {
		if got := recover(); got != want {
			t.Fatalf("panic = %v", got)
		}
	}()

	NewBalanceController(nil)

	t.Fatal("expected a panic")
}

func TestNew_BalanceController_KeepsTheWalletView(t *testing.T) {
	view := &walletview.Service{}

	if NewBalanceController(view).view != view {
		t.Fatal("balance controller did not keep the wallet view")
	}
}
