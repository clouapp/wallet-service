package jobs

import (
	"context"
	"time"

	"github.com/macrowallets/waas/app/services/refresh"
)

const reconcileWalletStateJob = "reconcile_wallet_state"

type ReconcileWalletState struct {
	wallets walletQueue
}

// NewReconcileWalletState reconciles one wallet against chain state.
func NewReconcileWalletState(refresher *refresh.WalletRefresher) *ReconcileWalletState {
	if refresher == nil {
		panic("reconcile_wallet_state: wallet refresher is required")
	}
	return &ReconcileWalletState{wallets: refresher}
}

func (j *ReconcileWalletState) Signature() string {
	return reconcileWalletStateJob
}

func (j *ReconcileWalletState) Handle(args ...any) error {
	payload, err := decodeWalletPayload(reconcileWalletStateJob, args)
	if err != nil {
		return err
	}
	return j.wallets.ReconcileWallet(context.Background(), payload.WalletID, payload.ChainID)
}

func (j *ReconcileWalletState) ShouldRetry(err error, attempt int) (bool, time.Duration) {
	return shouldRetry(err, attempt)
}
