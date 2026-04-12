package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/container"
)

type RefreshWalletTransactions struct{}

func (j *RefreshWalletTransactions) Signature() string {
	return "refresh_wallet_transactions"
}

func (j *RefreshWalletTransactions) Handle(args ...any) error {
	if len(args) < 2 {
		return fmt.Errorf("refresh_wallet_transactions: expected 2 args (wallet_id, chain_id)")
	}
	walletIDStr, ok0 := args[0].(string)
	chainID, ok1 := args[1].(string)
	if !ok0 || walletIDStr == "" {
		return fmt.Errorf("refresh_wallet_transactions: wallet_id must be a non-empty string")
	}
	if !ok1 || chainID == "" {
		return fmt.Errorf("refresh_wallet_transactions: chain_id must be a non-empty string")
	}

	walletID, err := uuid.Parse(walletIDStr)
	if err != nil {
		return fmt.Errorf("refresh_wallet_transactions: invalid wallet_id: %w", err)
	}

	c := container.Get()
	wallet, err := c.WalletRepo.FindByID(walletID)
	if err != nil {
		return fmt.Errorf("refresh_wallet_transactions: load wallet: %w", err)
	}
	if wallet == nil {
		return fmt.Errorf("refresh_wallet_transactions: wallet not found: %s", walletIDStr)
	}
	if wallet.Chain != chainID {
		return fmt.Errorf("refresh_wallet_transactions: chain_id %q does not match wallet chain %q", chainID, wallet.Chain)
	}

	slog.Info("refresh_wallet_transactions", "wallet", walletIDStr, "chain", chainID)
	// Transactions are refreshed as part of balance sync for now; dedicated service coming later
	if err := c.BalanceRefreshService.RefreshWallet(context.Background(), wallet); err != nil {
		return fmt.Errorf("refresh_wallet_transactions: %w", err)
	}
	return nil
}

func (j *RefreshWalletTransactions) ShouldRetry(err error, attempt int) (bool, time.Duration) {
	if attempt >= 5 {
		return false, 0
	}
	return true, time.Duration(attempt) * 5 * time.Second
}
