package jobs

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/container"
)

type RefreshWalletUTXOs struct{}

func (j *RefreshWalletUTXOs) Signature() string {
	return "refresh_wallet_utxos"
}

func (j *RefreshWalletUTXOs) Handle(args ...any) error {
	if len(args) < 2 {
		return fmt.Errorf("refresh_wallet_utxos: expected 2 args (wallet_id, chain_id)")
	}
	walletIDStr, ok0 := args[0].(string)
	chainID, ok1 := args[1].(string)
	if !ok0 || walletIDStr == "" {
		return fmt.Errorf("refresh_wallet_utxos: wallet_id must be a non-empty string")
	}
	if !ok1 || chainID == "" {
		return fmt.Errorf("refresh_wallet_utxos: chain_id must be a non-empty string")
	}

	walletID, err := uuid.Parse(walletIDStr)
	if err != nil {
		return fmt.Errorf("refresh_wallet_utxos: invalid wallet_id: %w", err)
	}

	c := container.Get()
	wallet, err := c.WalletRepo.FindByID(walletID)
	if err != nil {
		return fmt.Errorf("refresh_wallet_utxos: load wallet: %w", err)
	}
	if wallet == nil {
		return fmt.Errorf("refresh_wallet_utxos: wallet not found: %s", walletIDStr)
	}
	if wallet.Chain != chainID {
		return fmt.Errorf("refresh_wallet_utxos: chain_id %q does not match wallet chain %q", chainID, wallet.Chain)
	}

	// Chain-level UTXO fetching will be added per-provider; infrastructure is ready
	if chainID != "btc" && chainID != "tbtc" {
		slog.Warn("refresh_wallet_utxos: skipping non-Bitcoin chain", "wallet", walletIDStr, "chain", chainID)
		return nil
	}

	slog.Info("refresh_wallet_utxos: UTXO fetch from chain not yet implemented, infrastructure ready",
		"wallet", walletIDStr, "chain", chainID)
	return nil
}

func (j *RefreshWalletUTXOs) ShouldRetry(err error, attempt int) (bool, time.Duration) {
	if attempt >= 5 {
		return false, 0
	}
	return true, time.Duration(attempt) * 5 * time.Second
}
