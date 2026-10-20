package deposit

import (
	"context"
	"errors"
	"log/slog"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// RecordStreamedLedger records the deposits of a validated ledger the real-time XRP
// listener (app/services/xrpstream) heard about. The stream only names the ledger:
// its deposits are read through the chain's own RPC adapter like `scan:deposits
// --block` reads them (ScanBlock: the scanner's recording path and dedupe, and a
// ledger that keeps failing kept pending for the scanner's retry), so a stream
// endpoint on another network can never be the source of a deposit, and the ledger
// scan reaching the same ledger later records nothing again. The new deposits are
// then confirmed at that ledger, under the chain's scan lock (while a scan holds
// it, that scan's confirmation step confirms them); the balance refresh runs after
// the lock is released. It is meant for chains whose scanned blocks are final
// (XRP validated ledgers): a block that could still be reorganized would be
// confirmed by its own depth only. It returns how many deposits were new.
func (s *Service) RecordStreamedLedger(ctx context.Context, chainID string, ledger uint64) (int, error) {
	recorded, err := s.ScanBlock(ctx, chainID, ledger)
	if err != nil {
		return recorded, err
	}
	if recorded > 0 {
		s.confirmStreamedLedger(ctx, chainID, ledger)
	}
	return recorded, nil
}

// confirmStreamedLedger runs the confirmation step for the pending deposits of
// ledger only, at ledger, so a streamed payment never waits for the next scan
// cycle.
func (s *Service) confirmStreamedLedger(ctx context.Context, chainID string, ledger uint64) {
	adapter, err := s.registry.Chain(chainID)
	if err != nil {
		slog.Warn("streamed deposits left to the scan's confirmation step", "chain", chainID, "error", err)
		return
	}
	release, err := s.lockChain(chainID)
	if errors.Is(err, ErrScanInProgress) {
		return
	}
	if err != nil {
		slog.Warn("streamed deposits left to the scan's confirmation step", "chain", chainID, "error", err)
		return
	}
	confirmed := newWalletSet()
	s.confirmLedgerDeposits(ctx, adapter, ledger, confirmed)
	release()
	s.refreshBalances(ctx, confirmed.ids)
}

func (s *Service) confirmLedgerDeposits(ctx context.Context, adapter types.Chain, ledger uint64, confirmed *walletSet) {
	rows, err := s.txRepo.FindPendingByChain(ctx, adapter.ID())
	if err != nil {
		slog.Warn("streamed deposits left to the scan's confirmation step", "chain", adapter.ID(), "error", err)
		return
	}
	streamed := make([]models.Transaction, 0, len(rows))
	for _, row := range rows {
		if row.TxType == models.TxTypeDeposit && ledgerOf(row) == ledger {
			streamed = append(streamed, row)
		}
	}
	if err := s.confirmPending(ctx, adapter, ledger, streamed, confirmed); err != nil {
		slog.Warn("confirm streamed deposits", "chain", adapter.ID(), "error", err)
	}
}

// ledgerOf is the ledger (block) a row was recorded in, 0 for none.
func ledgerOf(tx models.Transaction) uint64 {
	if tx.BlockNumber <= 0 {
		return 0
	}
	return uint64(tx.BlockNumber)
}
