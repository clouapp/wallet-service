package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/container"
)

type RefreshWalletBalances struct{}

func (j *RefreshWalletBalances) Signature() string {
	return "refresh_wallet_balances"
}

func (j *RefreshWalletBalances) Handle(args ...any) error {
	if len(args) < 2 {
		return fmt.Errorf("refresh_wallet_balances: expected 2 args (wallet_id, chain_id)")
	}
	walletIDStr, ok0 := args[0].(string)
	chainID, ok1 := args[1].(string)
	if !ok0 || walletIDStr == "" {
		return fmt.Errorf("refresh_wallet_balances: wallet_id must be a non-empty string")
	}
	if !ok1 || chainID == "" {
		return fmt.Errorf("refresh_wallet_balances: chain_id must be a non-empty string")
	}

	walletID, err := uuid.Parse(walletIDStr)
	if err != nil {
		return fmt.Errorf("refresh_wallet_balances: invalid wallet_id: %w", err)
	}

	c := container.Get()
	wallet, err := c.WalletRepo.FindByID(walletID)
	if err != nil {
		return fmt.Errorf("refresh_wallet_balances: load wallet: %w", err)
	}
	if wallet == nil {
		return fmt.Errorf("refresh_wallet_balances: wallet not found: %s", walletIDStr)
	}
	if wallet.Chain != chainID {
		return fmt.Errorf("refresh_wallet_balances: chain_id %q does not match wallet chain %q", chainID, wallet.Chain)
	}

	slog.Info("refresh_wallet_balances", "wallet", walletIDStr, "chain", chainID)
	if err := c.BalanceRefreshService.RefreshWallet(context.Background(), wallet); err != nil {
		return fmt.Errorf("refresh_wallet_balances: %w", err)
	}
	return nil
}

func (j *RefreshWalletBalances) ShouldRetry(err error, attempt int) (bool, time.Duration) {
	if attempt >= 5 {
		return false, 0
	}
	return true, time.Duration(attempt) * 5 * time.Second
}
