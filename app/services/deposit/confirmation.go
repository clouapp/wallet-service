package deposit

import (
	"context"
	"log/slog"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/blockheight"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

const blockHeightProviderFailureThreshold = 3

func adapterBlockHeightKind(adapter types.Chain) string {
	switch adapter.(type) {
	case *chain.EVMLive:
		return models.AdapterTypeEVM
	case *chain.BitcoinLive:
		return models.AdapterTypeBitcoin
	case *chain.SolanaLive:
		return models.AdapterTypeSolana
	default:
		return ""
	}
}

func (s *Service) providerForAdapter(adapter types.Chain) blockheight.Provider {
	if s.blockHeightProviders == nil {
		return nil
	}
	key := adapterBlockHeightKind(adapter)
	if key == "" {
		return nil
	}
	return s.blockHeightProviders[key]
}

// resolveCurrentBlockHeight returns the chain tip for confirmation math. When a free
// block-height provider is configured, it is preferred; after repeated provider failures
// the adapter RPC is used as a degraded fallback.
func (s *Service) resolveCurrentBlockHeight(ctx context.Context, chainID string, adapter types.Chain) (uint64, bool) {
	provider := s.providerForAdapter(adapter)
	if provider == nil {
		h, err := adapter.GetLatestBlock(ctx)
		if err != nil {
			slog.Warn("get latest block (no block height provider)", "chain", chainID, "error", err)
			return 0, false
		}
		s.heightFailures[chainID] = 0
		return h, true
	}

	h, err := provider.GetBlockHeight(ctx, chainID)
	if err == nil {
		s.heightFailures[chainID] = 0
		return h, true
	}

	slog.Warn("block height provider failed", "chain", chainID, "error", err)
	failures := s.heightFailures[chainID] + 1
	s.heightFailures[chainID] = failures
	if failures < blockHeightProviderFailureThreshold {
		return 0, false
	}

	slog.Warn("block height degraded to adapter get latest block", "chain", chainID)
	h2, err2 := adapter.GetLatestBlock(ctx)
	if err2 != nil {
		slog.Warn("degraded get latest block failed", "chain", chainID, "error", err2)
		return 0, false
	}
	s.heightFailures[chainID] = 0
	return h2, true
}

func (s *Service) updateConfirmations(ctx context.Context, chainID string, adapter types.Chain, currentBlock uint64) error {
	pending, err := s.txRepo.FindPendingByChain(chainID)
	if err != nil {
		return err
	}

	for _, tx := range pending {
		if tx.BlockNumber == 0 {
			continue
		}
		confs := int(currentBlock) - int(tx.BlockNumber)
		if confs < 0 {
			confs = 0
		}

		newStatus := string(types.TxStatusConfirming)
		var confirmedAt *time.Time
		if confs >= tx.RequiredConfs {
			newStatus = string(types.TxStatusConfirmed)
			now := time.Now().UTC()
			confirmedAt = &now
		}

		if err := s.txRepo.UpdateFields(tx.ID, map[string]interface{}{
			"confirmations": confs,
			"status":        newStatus,
			"confirmed_at":  confirmedAt,
		}); err != nil {
			slog.Error("update confs", "tx_id", tx.ID, "error", err)
			continue
		}

		if newStatus == string(types.TxStatusConfirmed) && tx.Status != string(types.TxStatusConfirmed) {
			s.webhookSvc.EnqueueEvent(ctx, tx.ID, types.EventDepositConfirmed, tx)
		} else if tx.Status == string(types.TxStatusPending) && newStatus == string(types.TxStatusConfirming) {
			s.webhookSvc.EnqueueEvent(ctx, tx.ID, types.EventDepositConfirming, tx)
		}
	}
	return nil
}

// RunConfirmationCheck walks all registered chains that have pending deposits and
// refreshes confirmation counts using free block-height APIs when configured.
func (s *Service) RunConfirmationCheck(ctx context.Context) error {
	for _, chainID := range s.registry.ChainIDs() {
		pending, err := s.txRepo.FindPendingByChain(chainID)
		if err != nil {
			slog.Error("find pending by chain", "chain", chainID, "error", err)
			continue
		}
		if len(pending) == 0 {
			continue
		}

		adapter, err := s.registry.Chain(chainID)
		if err != nil {
			slog.Error("registry chain", "chain", chainID, "error", err)
			continue
		}

		height, ok := s.resolveCurrentBlockHeight(ctx, chainID, adapter)
		if !ok {
			continue
		}

		if err := s.updateConfirmations(ctx, chainID, adapter, height); err != nil {
			slog.Error("update confirmations failed", "chain", chainID, "error", err)
		}
	}
	return nil
}
