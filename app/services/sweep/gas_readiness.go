package sweep

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// RefreshGasStatus computes the on-chain gas-readiness status for walletID and
// persists it on the wallet row. Emits wallet.gas_status.changed when the
// status transitions. gas_last_checked_at is updated on every call.
//
// Bitcoin short-circuits to "seeded" because fees come from the UTXO being
// spent — there is no separate gas asset to monitor. A chain whose
// gas_readiness_threshold_raw is empty is also treated as always-seeded so
// downstream guards do not block it. Production does not substitute an
// environment default.
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

	chainEntity, err := s.loadChain(ctx, wallet.Chain)
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
		threshold = s.fallbackGasThreshold(chainEntity.ID)
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

// gasStatusStager inserts wallet.gas_status.changed webhook rows on the caller's
// transaction. The returned send runs only after that transaction commits.
type gasStatusStager interface {
	StageWalletGasStatusChanged(ctx context.Context, walletID uuid.UUID, data interface{}) (func(context.Context), error)
}

// persistGasStatus writes gas_last_checked_at on every call and gas_status only
// on transition. A transition with a webhook service commits the wallet update
// and the wallet.gas_status.changed row together. A failed webhook insert rolls
// the wallet update back. The queue send runs only after that commit. A check
// that does not transition, or a service with no webhook writer, writes the
// wallet row alone.
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

	var send func(context.Context)
	write := func(writeCtx context.Context) error {
		if err := s.walletRepo.RecordGasCheck(writeCtx, wallet.ID, now, newStatus, transitioned); err != nil {
			return fmt.Errorf("sweep: update wallet gas status: %w", err)
		}
		if !transitioned || s.webhookSvc == nil {
			return nil
		}
		stager, ok := s.webhookSvc.(gasStatusStager)
		if !ok {
			return fmt.Errorf("persist gas status webhook: event writer cannot join the transaction")
		}
		staged, stageErr := stager.StageWalletGasStatusChanged(writeCtx, wallet.ID, gasStatusPayload(wallet, chainEntity, oldStatus, newStatus, balance, threshold, baseAddr))
		if stageErr != nil {
			return fmt.Errorf("persist gas status webhook: %w", stageErr)
		}
		send = staged
		return nil
	}

	if transitioned && s.webhookSvc != nil {
		if err := s.walletRepo.Within(ctx, write); err != nil {
			return nil, err
		}
		if send != nil {
			send(ctx)
		}
	} else if err := write(ctx); err != nil {
		return nil, err
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

func gasStatusPayload(
	wallet *models.Wallet,
	chainEntity *models.Chain,
	oldStatus, newStatus string,
	balance, threshold *big.Int,
	baseAddr string,
) map[string]interface{} {
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
	return payload
}

// fallbackGasThreshold returns the injected default threshold for a chain when
// the chains row has no value. nil means the chain has no gas concept.
func (s *service) fallbackGasThreshold(chainID string) *big.Int {
	if s == nil || len(s.gasDefaults) == 0 {
		return nil
	}
	d, ok := s.gasDefaults[chainID]
	if !ok || d.Raw == "" {
		return nil
	}
	v, ok := new(big.Int).SetString(d.Raw, 10)
	if !ok {
		return nil
	}
	return v
}
