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

const (
	blockHeightProviderFailureThreshold = 3
	withdrawalBackfillBatchSize         = 50
	allTxTypes                          = ""
)

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

// isOutboundTxType reports whether a tx type was broadcast by this service
// (sweep / withdrawal / gas_seed). Outbound rows are inserted with
// block_number=0 and must be reconciled against the chain before the
// confirmation math can run.
func isOutboundTxType(txType string) bool {
	switch txType {
	case models.TxTypeSweep, models.TxTypeWithdrawal, models.TxTypeGasSeed:
		return true
	}
	return false
}

func (s *Service) updateConfirmations(ctx context.Context, chainID string, adapter types.Chain, currentBlock uint64) error {
	pending, err := s.txRepo.FindPendingByChain(chainID)
	if err != nil {
		return err
	}
	return s.applyConfirmations(ctx, adapter, currentBlock, pending)
}

func (s *Service) applyConfirmations(ctx context.Context, adapter types.Chain, currentBlock uint64, pending []models.Transaction) error {
	for _, tx := range pending {
		if tx.BlockNumber == 0 {
			if !isOutboundTxType(tx.TxType) {
				continue
			}
			block, berr := adapter.GetTransactionBlock(ctx, tx.TxHash)
			if berr != nil {
				slog.Warn("reconcile block number", "tx_id", tx.ID, "tx_hash", tx.TxHash, "error", berr)
				continue
			}
			if block == 0 {
				continue
			}
			tx.BlockNumber = int64(block)
			if err := s.txRepo.UpdateFields(tx.ID, map[string]interface{}{"block_number": block}); err != nil {
				slog.Error("persist block number", "tx_id", tx.ID, "error", err)
				continue
			}
		}
		confs := confirmationsAt(currentBlock, tx.BlockNumber)

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

		confirmedNow := newStatus == string(types.TxStatusConfirmed) && tx.Status != string(types.TxStatusConfirmed)
		confirmingNow := tx.Status == string(types.TxStatusPending) && newStatus == string(types.TxStatusConfirming)

		switch tx.TxType {
		case models.TxTypeDeposit:
			if confirmedNow {
				s.publishDeposit(ctx, types.EventDepositConfirmed, withConfirmationState(tx, confs, newStatus, confirmedAt))
			} else if confirmingNow {
				s.publishDeposit(ctx, types.EventDepositConfirming, withConfirmationState(tx, confs, newStatus, confirmedAt))
			}
		case models.TxTypeSweep:
			if confirmedNow {
				s.webhookSvc.EnqueueEvent(ctx, tx.ID, types.EventSweepConfirmed, tx)
			}
		case models.TxTypeWithdrawal:
			if confirmedNow {
				s.publishWithdrawalConfirmed(ctx, tx, confs, newStatus, confirmedAt)
			}
		case models.TxTypeGasSeed:
		}
	}
	return nil
}

// confirmationsAt counts the block that includes the transaction as its first
// confirmation: a transaction in the tip block has 1. A block the tip has not reached
// yet (a lagging height provider) counts 0.
func confirmationsAt(currentBlock uint64, txBlock int64) int {
	if txBlock <= 0 || uint64(txBlock) > currentBlock {
		return 0
	}
	return int(currentBlock-uint64(txBlock)) + 1
}

// withConfirmationState returns the transaction as just persisted by the tracker.
func withConfirmationState(tx models.Transaction, confs int, status string, confirmedAt *time.Time) models.Transaction {
	tx.Confirmations = confs
	tx.Status = status
	tx.ConfirmedAt = confirmedAt
	return tx
}

func (s *Service) publishWithdrawalConfirmed(ctx context.Context, tx models.Transaction, confs int, status string, confirmedAt *time.Time) {
	tx = withConfirmationState(tx, confs, status, confirmedAt)
	if s.withdrawals == nil {
		s.webhookSvc.EnqueueEvent(ctx, tx.ID, types.EventWithdrawalConfirmed, tx)
		return
	}
	if err := s.withdrawals.MarkConfirmed(ctx, &tx); err != nil {
		slog.Error("publish withdrawal confirmed", "tx_id", tx.ID, "error", err)
	}
}

// RunConfirmationCheck walks all registered chains that have pending deposits and
// refreshes confirmation counts using free block-height APIs when configured.
func (s *Service) RunConfirmationCheck(ctx context.Context) error {
	s.runConfirmationCheck(ctx, allTxTypes)
	s.backfillWithdrawalConfirmations(ctx)
	return nil
}

// RunWithdrawalConfirmationCheck is the local-mode tracker: it only advances outbound
// withdrawal transactions, so deposits, sweeps and gas seeds keep waiting for the
// real confirmation_tracker and no deposit webhook is emitted from a dev machine.
func (s *Service) RunWithdrawalConfirmationCheck(ctx context.Context) error {
	s.runConfirmationCheck(ctx, models.TxTypeWithdrawal)
	s.backfillWithdrawalConfirmations(ctx)
	return nil
}

func (s *Service) runConfirmationCheck(ctx context.Context, onlyTxType string) {
	for _, chainID := range s.registry.ChainIDs() {
		pending, err := s.txRepo.FindPendingByChain(chainID)
		if err != nil {
			slog.Error("find pending by chain", "chain", chainID, "error", err)
			continue
		}
		pending = filterByTxType(pending, onlyTxType)
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

		if err := s.applyConfirmations(ctx, adapter, height, pending); err != nil {
			slog.Error("update confirmations failed", "chain", chainID, "error", err)
		}
	}
}

func (s *Service) backfillWithdrawalConfirmations(ctx context.Context) {
	if s.withdrawals == nil {
		return
	}
	confirmed, err := s.withdrawals.Backfill(ctx, withdrawalBackfillBatchSize)
	if err != nil {
		slog.Error("backfill withdrawal confirmations", "error", err)
		return
	}
	if confirmed > 0 {
		slog.Info("withdrawal confirmations backfilled", "count", confirmed)
	}
}

func filterByTxType(transactions []models.Transaction, txType string) []models.Transaction {
	if txType == allTxTypes {
		return transactions
	}
	filtered := make([]models.Transaction, 0, len(transactions))
	for _, tx := range transactions {
		if tx.TxType == txType {
			filtered = append(filtered, tx)
		}
	}
	return filtered
}
