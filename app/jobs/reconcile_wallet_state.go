package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/container"
)

type ReconcileWalletState struct{}

func (j *ReconcileWalletState) Signature() string {
	return "reconcile_wallet_state"
}

func (j *ReconcileWalletState) Handle(args ...any) error {
	if len(args) < 2 {
		return fmt.Errorf("reconcile_wallet_state: expected 2 args (wallet_id, chain_id)")
	}
	walletIDStr, ok0 := args[0].(string)
	chainID, ok1 := args[1].(string)
	if !ok0 || walletIDStr == "" {
		return fmt.Errorf("reconcile_wallet_state: wallet_id must be a non-empty string")
	}
	if !ok1 || chainID == "" {
		return fmt.Errorf("reconcile_wallet_state: chain_id must be a non-empty string")
	}

	walletID, err := uuid.Parse(walletIDStr)
	if err != nil {
		return fmt.Errorf("reconcile_wallet_state: invalid wallet_id: %w", err)
	}

	c := container.Get()
	wallet, err := c.WalletRepo.FindByID(walletID)
	if err != nil {
		return fmt.Errorf("reconcile_wallet_state: load wallet: %w", err)
	}
	if wallet == nil {
		return fmt.Errorf("reconcile_wallet_state: wallet not found: %s", walletIDStr)
	}
	if wallet.Chain != chainID {
		return fmt.Errorf("reconcile_wallet_state: chain_id %q does not match wallet chain %q", chainID, wallet.Chain)
	}

	slog.Info("reconcile_wallet_state", "wallet", walletIDStr, "chain", chainID)
	// Full reconciliation compares chain state vs DB; for now runs a fresh balance sync
	if err := c.BalanceRefreshService.RefreshWallet(context.Background(), wallet); err != nil {
		return fmt.Errorf("reconcile_wallet_state: %w", err)
	}
	return nil
}

func (j *ReconcileWalletState) ShouldRetry(err error, attempt int) (bool, time.Duration) {
	if attempt >= 5 {
		return false, 0
	}
	return true, time.Duration(attempt) * 5 * time.Second
}
