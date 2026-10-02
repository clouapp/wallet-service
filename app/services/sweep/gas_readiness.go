package sweep

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/config"
	"github.com/macrowallets/waas/pkg/types"
)

// RefreshGasStatus computes the on-chain gas-readiness status for walletID and
// persists it on the wallet row. Emits types.EventWalletGasStatusChanged when
// the status transitions. gas_last_checked_at is updated on every call.
//
// Bitcoin short-circuits to "seeded" because fees come from the UTXO being
// spent — there is no separate gas asset to monitor. Chains whose threshold
// cannot be resolved (row is NULL and config.SweepDefaults has no entry) are
// also treated as always-seeded so downstream guards do not block them.
func (s *service) RefreshGasStatus(ctx context.Context, walletID uuid.UUID) (*GasStatus, error) {
	wallet, err := s.walletRepo.FindByID(ctx, walletID)
	if err != nil {
		return nil, fmt.Errorf("sweep: find wallet: %w", err)
	}
	if wallet == nil {
		return nil, fmt.Errorf("sweep: wallet %s not found", walletID)
	}
	if wallet.DepositAddress == nil {
		return nil, fmt.Errorf("sweep: wallet %s has no base deposit address", walletID)
	}

	chainEntity, err := s.chainRepo.FindByID(wallet.Chain)
	if err != nil {
		return nil, fmt.Errorf("sweep: find chain %q: %w", wallet.Chain, err)
	}
	if chainEntity == nil {
		return nil, fmt.Errorf("sweep: chain %q not found", wallet.Chain)
	}

	baseAddr := wallet.DepositAddress.Address

	// Bitcoin has no gas concept.
	if chainEntity.AdapterType == models.AdapterTypeBitcoin {
		return s.persistGasStatus(ctx, wallet, chainEntity, models.GasStatusSeeded, nil, nil, baseAddr)
	}

	threshold := chainEntity.GasReadinessThreshold()
	if threshold == nil {
		threshold = fallbackGasThreshold(chainEntity.ID)
	}
	// No threshold anywhere → treat as always-seeded (chain has no gas concept).
	if threshold == nil {
		return s.persistGasStatus(ctx, wallet, chainEntity, models.GasStatusSeeded, nil, nil, baseAddr)
	}

	adapter, err := s.registry.Chain(wallet.Chain)
	if err != nil {
		return nil, fmt.Errorf("sweep: adapter not registered for %q: %w", wallet.Chain, err)
	}
	bal, err := adapter.GetBalance(ctx, baseAddr)
	if err != nil {
		return nil, fmt.Errorf("sweep: get balance: %w", err)
	}

	var balance *big.Int
	if bal != nil && bal.Amount != nil {
		balance = new(big.Int).Set(bal.Amount)
	} else {
		balance = new(big.Int)
	}

	var newStatus string
	switch {
	case balance.Sign() == 0:
		newStatus = models.GasStatusUnseeded
	case balance.Cmp(threshold) < 0:
		newStatus = models.GasStatusLow
	default:
		newStatus = models.GasStatusSeeded
	}

	return s.persistGasStatus(ctx, wallet, chainEntity, newStatus, balance, threshold, baseAddr)
}

// persistGasStatus writes gas_last_checked_at on every call and gas_status only
// on transition. Emits EventWalletGasStatusChanged on transition when a webhook
// service is wired.
func (s *service) persistGasStatus(
	ctx context.Context,
	wallet *models.Wallet,
	chainEntity *models.Chain,
	newStatus string,
	balance, threshold *big.Int,
	baseAddr string,
) (*GasStatus, error) {
	now := time.Now().UTC()
	oldStatus := wallet.GasStatus
	transitioned := oldStatus != newStatus

	if err := s.walletRepo.RecordGasCheck(ctx, wallet.ID, now, newStatus, transitioned); err != nil {
		return nil, fmt.Errorf("sweep: update wallet gas status: %w", err)
	}

	if transitioned && s.webhookSvc != nil {
		payload := map[string]interface{}{
			"wallet_id":    wallet.ID.String(),
			"chain":        wallet.Chain,
			"old_status":   oldStatus,
			"new_status":   newStatus,
			"base_address": baseAddr,
			"native_asset": chainEntity.NativeSymbol,
		}
		if balance != nil {
			payload["native_balance"] = balance.String()
		}
		if threshold != nil {
			payload["threshold"] = threshold.String()
		}
		s.webhookSvc.EnqueueEvent(ctx, wallet.ID, types.EventWalletGasStatusChanged, payload)
	}

	return &GasStatus{
		Status:        newStatus,
		BaseAddress:   baseAddr,
		NativeAsset:   chainEntity.NativeSymbol,
		NativeBalance: balance,
		Threshold:     threshold,
		LastCheckedAt: now.Unix(),
	}, nil
}

// fallbackGasThreshold returns the env-configured default threshold for a chain
// when the chains row has no value. nil means the chain has no gas concept.
func fallbackGasThreshold(chainID string) *big.Int {
	defaults := config.SweepDefaults()
	d, ok := defaults[chainID]
	if !ok || d.GasReadinessRaw == "" {
		return nil
	}
	v, ok := new(big.Int).SetString(d.GasReadinessRaw, 10)
	if !ok {
		return nil
	}
	return v
}
