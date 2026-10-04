package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

type RefreshWalletTokens struct {
	balances *refresh.BalanceService
}

// NewRefreshWalletTokens refreshes one wallet's token balances.
func NewRefreshWalletTokens(balances *refresh.BalanceService) *RefreshWalletTokens {
	if balances == nil {
		panic("refresh_wallet_tokens: balance refresh service is required")
	}
	return &RefreshWalletTokens{balances: balances}
}

func (j *RefreshWalletTokens) Signature() string {
	return "refresh_wallet_tokens"
}

func (j *RefreshWalletTokens) Handle(args ...any) error {
	if len(args) < 2 {
		return fmt.Errorf("refresh_wallet_tokens: expected 2 args (wallet_id, chain_id)")
	}
	walletIDStr, ok0 := args[0].(string)
	chainID, ok1 := args[1].(string)
	if !ok0 || walletIDStr == "" {
		return fmt.Errorf("refresh_wallet_tokens: wallet_id must be a non-empty string")
	}
	if !ok1 || chainID == "" {
		return fmt.Errorf("refresh_wallet_tokens: chain_id must be a non-empty string")
	}

	walletID, err := uuid.Parse(walletIDStr)
	if err != nil {
		return fmt.Errorf("refresh_wallet_tokens: invalid wallet_id: %w", err)
	}

	wallet, err := container.MustMake[*walletrecords.Wallets]().FindByID(context.Background(), walletID)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		return fmt.Errorf("refresh_wallet_tokens: load wallet: %w", err)
	}
	if wallet == nil || errors.Is(err, models.ErrRepositoryNotFound) {
		return fmt.Errorf("refresh_wallet_tokens: wallet not found: %s", walletIDStr)
	}
	if wallet.Chain != chainID {
		return fmt.Errorf("refresh_wallet_tokens: chain_id %q does not match wallet chain %q", chainID, wallet.Chain)
	}

	if j.balances == nil {
		return fmt.Errorf("refresh_wallet_tokens: balance refresh service is not initialized")
	}
	slog.Info("refresh_wallet_tokens", "wallet", walletIDStr, "chain", chainID)
	// Token balances are refreshed as part of BalanceService.RefreshWallet
	if err := j.balances.RefreshWallet(context.Background(), wallet); err != nil {
		return fmt.Errorf("refresh_wallet_tokens: %w", err)
	}
	return nil
}

func (j *RefreshWalletTokens) ShouldRetry(err error, attempt int) (bool, time.Duration) {
	if attempt >= 5 {
		return false, 0
	}
	return true, time.Duration(attempt) * 5 * time.Second
}
