package deposit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/blockheight"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	blockHeightProviderFailureThreshold = 3
	withdrawalBackfillBatchSize         = 50
	allTxTypes                          = ""
)

func adapterBlockHeightKind(adapter types.Chain) string {
	if _, ok := adapter.(evmNetwork); ok {
		return models.AdapterTypeEVM
	}
	if _, ok := adapter.(solanaNetwork); ok {
		return models.AdapterTypeSolana
	}
	if _, ok := adapter.(bitcoinNetwork); ok {
		return models.AdapterTypeBitcoin
	}
	return ""
}

// evmNetwork is the port the live EVM client already satisfies.
type evmNetwork interface {
	EstimateL1DataFee(ctx context.Context, req types.TransferRequest) (*big.Int, error)
}

// solanaNetwork is the port the live Solana client already satisfies.
type solanaNetwork interface {
	SignTransactionWithScalar(ctx context.Context, unsigned *types.UnsignedTx, scalar, publicKey []byte) (*types.SignedTx, error)
}

// bitcoinNetwork is the port the live Bitcoin client already satisfies.
type bitcoinNetwork interface {
	IsTestnet() bool
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
		return s.adapterBlockHeight(ctx, chainID, adapter, "no block height provider")
	}

	h, err := provider.GetBlockHeight(ctx, chainID)
	if err == nil {
		s.heightFailures[chainID] = 0
		return h, true
	}
	if errors.Is(err, blockheight.ErrTipFromChainRPC) {
		return s.adapterBlockHeight(ctx, chainID, adapter, "network served by the chain RPC")
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

func (s *Service) adapterBlockHeight(ctx context.Context, chainID string, adapter types.Chain, reason string) (uint64, bool) {
	h, err := adapter.GetLatestBlock(ctx)
	if err != nil {
		slog.Warn("get latest block ("+reason+")", "chain", chainID, "error", err)
		return 0, false
	}
	s.heightFailures[chainID] = 0
	return h, true
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
	pending, err := s.txRepo.FindPendingByChain(ctx, chainID)
	if err != nil {
		return err
	}
	return s.applyConfirmations(ctx, adapter, currentBlock, pending)
}

func (s *Service) applyConfirmations(ctx context.Context, adapter types.Chain, currentBlock uint64, pending []models.Transaction) error {
	confirmedWallets := newWalletSet()
	defer func() { s.refreshBalances(ctx, confirmedWallets.ids) }()
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
			if err := s.txRepo.SetBlockNumber(ctx, tx.ID, block); err != nil {
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

		confirmedNow := newStatus == string(types.TxStatusConfirmed) && tx.Status != string(types.TxStatusConfirmed)
		if tx.TxType == models.TxTypeSweep && confirmedNow {
			if err := s.persistSweepConfirmed(ctx, tx, confs, newStatus, confirmedAt); err != nil {
				slog.Error("confirm sweep", "tx_id", tx.ID, "error", err)
				continue
			}
			confirmedWallets.add(tx.WalletID)
			continue
		}

		if err := s.txRepo.RecordConfirmations(ctx, tx.ID, confs, newStatus, confirmedAt); err != nil {
			slog.Error("update confs", "tx_id", tx.ID, "error", err)
			continue
		}

		confirmingNow := tx.Status == string(types.TxStatusPending) && newStatus == string(types.TxStatusConfirming)
		if confirmedNow && movesWalletBalance(tx.TxType) {
			confirmedWallets.add(tx.WalletID)
		}

		switch tx.TxType {
		case models.TxTypeDeposit:
			if confirmedNow {
				s.publishDeposit(ctx, types.EventDepositConfirmed, withConfirmationState(tx, confs, newStatus, confirmedAt))
			} else if confirmingNow {
				s.publishDeposit(ctx, types.EventDepositConfirming, withConfirmationState(tx, confs, newStatus, confirmedAt))
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

// confirmationTx opens one transaction. Queries that use the callback context
// join it, including the sweep.confirmed webhook row.
type confirmationTx interface {
	Within(ctx context.Context, fn func(context.Context) error) error
}

// persistSweepConfirmed writes the confirmation and the sweep.confirmed webhook
// row in one transaction. A failed webhook insert rolls the confirmation back.
// The queue send runs only after that commit. With no webhook writer, only the
// transaction row is written.
func (s *Service) persistSweepConfirmed(ctx context.Context, tx models.Transaction, confs int, status string, confirmedAt *time.Time) error {
	if s.webhookSvc == nil {
		return s.txRepo.RecordConfirmations(ctx, tx.ID, confs, status, confirmedAt)
	}
	joiner, ok := s.txRepo.(confirmationTx)
	if !ok {
		return fmt.Errorf("confirm sweep: transaction writer cannot open a transaction")
	}
	var send func(context.Context)
	err := joiner.Within(ctx, func(txCtx context.Context) error {
		if err := s.txRepo.RecordConfirmations(txCtx, tx.ID, confs, status, confirmedAt); err != nil {
			return fmt.Errorf("confirm sweep: %w", err)
		}
		staged, stageErr := s.webhookSvc.StageSweepConfirmed(txCtx, &tx)
		if stageErr != nil {
			return fmt.Errorf("confirm sweep webhook: %w", stageErr)
		}
		send = staged
		return nil
	})
	if err != nil {
		return err
	}
	if send != nil {
		send(ctx)
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
		pending, err := s.txRepo.FindPendingByChain(ctx, chainID)
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
