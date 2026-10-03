package deposit

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/tests/mocks"
)

type recordingRefresher struct {
	mu      sync.Mutex
	wallets []uuid.UUID
	err     error
}

func (r *recordingRefresher) RefreshWalletByID(_ context.Context, walletID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.wallets = append(r.wallets, walletID)
	return r.err
}

func TestUpdateConfirmations_RefreshesEachWalletWhoseMovementJustConfirmed(t *testing.T) {
	svc, adapter, _ := setupDepositService(t)
	refresher := &recordingRefresher{err: errors.New("rpc down")}
	svc.SetBalanceRefresher(refresher)

	depositWallet := mocks.InsertWallet(t, "eth")
	withdrawWallet := mocks.InsertWallet(t, "eth")
	quietWallet := mocks.InsertWallet(t, "eth")
	mocks.InsertTransaction(t, depositWallet.ID, nil, "eth", models.TxTypeDeposit, "pending", "eth", "1000", 100)
	mocks.InsertTransaction(t, depositWallet.ID, nil, "eth", models.TxTypeSweep, "pending", "eth", "900", 100)
	mocks.InsertTransaction(t, withdrawWallet.ID, nil, "eth", models.TxTypeWithdrawal, "pending", "eth", "500", 100)
	mocks.InsertTransaction(t, quietWallet.ID, nil, "eth", models.TxTypeDeposit, "pending", "eth", "700", 109)

	if err := svc.updateConfirmations(context.Background(), "eth", adapter, 110); err != nil {
		t.Fatal(err)
	}
	if len(refresher.wallets) != 2 || refresher.wallets[0] != depositWallet.ID || refresher.wallets[1] != withdrawWallet.ID {
		t.Fatalf("expected one refresh per wallet with a confirmed movement, got %v", refresher.wallets)
	}

	if err := svc.updateConfirmations(context.Background(), "eth", adapter, 110); err != nil {
		t.Fatal(err)
	}
	if len(refresher.wallets) != 2 {
		t.Fatalf("movements already confirmed must not refresh again, got %v", refresher.wallets)
	}
	if err := svc.updateConfirmations(context.Background(), "eth", adapter, 112); err != nil {
		t.Fatal(err)
	}
	if len(refresher.wallets) != 3 || refresher.wallets[2] != quietWallet.ID {
		t.Fatalf("the later deposit refreshes its wallet once it confirms, got %v", refresher.wallets)
	}
}
