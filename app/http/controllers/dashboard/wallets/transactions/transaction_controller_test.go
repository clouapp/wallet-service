package transactions

import (
	"testing"

	"github.com/macrowallets/waas/app/services/walletview"
)

func TestNew_TransactionController_RequiresTheWalletView(t *testing.T) {
	const want = "dashboard wallet transactions controller: wallet view is required"
	defer func() {
		if got := recover(); got != want {
			t.Fatalf("panic = %v", got)
		}
	}()

	NewTransactionController(nil)

	t.Fatal("expected a panic")
}

func TestNew_TransactionController_KeepsTheWalletView(t *testing.T) {
	view := &walletview.Service{}

	if NewTransactionController(view).view != view {
		t.Fatal("transaction controller did not keep the wallet view")
	}
}
