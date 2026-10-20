package deposit

import (
	"context"
	"errors"
	"log/slog"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// RecordValidatedTransfers records the deposits a chain stream reported in final
// ledgers (the real-time XRP listener, app/services/xrpstream) through the
// scanner's own path, recordTransfers: the same watched-address check and one
// deposit per chain and transaction, so the ledger scan that reaches the same
// ledger later records nothing again (it stays the guarantee). The new deposits
// are then confirmed at head, the newest validated ledger the stream has seen,
// under the chain's scan lock; while a scan holds it, that scan's confirmation
// step confirms them. It returns how many deposits were new.
func (s *Service) RecordValidatedTransfers(ctx context.Context, chainID string, head uint64, transfers []types.DetectedTransfer) (int, error) {
	adapter, err := s.registry.Chain(chainID)
	if err != nil {
		return 0, err
	}
	recorded, _, err := s.recordTransfers(ctx, chainID, adapter, transfers)
	if recorded > 0 {
		s.confirmStreamed(ctx, chainID, adapter, head, transfers)
	}
	return recorded, err
}

// confirmStreamed runs the confirmation step for the pending deposits of
// transfers only, so a streamed payment never waits for the next scan cycle.
func (s *Service) confirmStreamed(ctx context.Context, chainID string, adapter types.Chain, head uint64, transfers []types.DetectedTransfer) {
	release, err := s.lockChain(chainID)
	if errors.Is(err, ErrScanInProgress) {
		return
	}
	if err != nil {
		slog.Warn("streamed deposits left to the scan's confirmation step", "chain", chainID, "error", err)
		return
	}
	defer release()
	rows, err := s.txRepo.FindPendingByChain(ctx, chainID)
	if err != nil {
		slog.Warn("streamed deposits left to the scan's confirmation step", "chain", chainID, "error", err)
		return
	}
	streamed := make([]models.Transaction, 0, len(transfers))
	for _, row := range rows {
		if row.TxType == models.TxTypeDeposit && reportsTx(transfers, row.TxHash) {
			streamed = append(streamed, row)
		}
	}
	if err := s.applyConfirmations(ctx, adapter, head, streamed); err != nil {
		slog.Warn("confirm streamed deposits", "chain", chainID, "error", err)
	}
}

func reportsTx(transfers []types.DetectedTransfer, txHash string) bool {
	for _, transfer := range transfers {
		if sameTxHash(transfer.TxHash, txHash) {
			return true
		}
	}
	return false
}
