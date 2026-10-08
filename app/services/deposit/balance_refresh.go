package deposit

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// WalletBalanceRefresher re-reads a wallet's balances from the chain into the read model.
type WalletBalanceRefresher interface {
	RefreshWalletByID(ctx context.Context, walletID uuid.UUID) error
}

// SetBalanceRefresher wires the balance read model refresh run once a deposit,
// withdrawal or sweep of a wallet reaches its confirmations.
func (s *Service) SetBalanceRefresher(balances WalletBalanceRefresher) {
	s.balances = balances
}

func movesWalletBalance(txType string) bool {
	switch txType {
	case models.TxTypeDeposit, models.TxTypeWithdrawal, models.TxTypeSweep:
		return true
	}
	return false
}

// refreshBalances refreshes each wallet once; a failure only leaves the read model
// stale until the next refresh, so it is logged and the others still run.
func (s *Service) refreshBalances(ctx context.Context, walletIDs []uuid.UUID) {
	if s.balances == nil || len(walletIDs) == 0 {
		return
	}
	for _, walletID := range walletIDs {
		if ctx.Err() != nil {
			return
		}
		if err := s.balances.RefreshWalletByID(ctx, walletID); err != nil {
			slog.Error("balance refresh after confirmation failed", "wallet", walletID, "error", err)
			continue
		}
		slog.Info("balance refreshed after confirmation", "wallet", walletID)
	}
}

// walletSet keeps wallet ids in first-seen order without repeats.
type walletSet struct {
	ids  []uuid.UUID
	seen map[uuid.UUID]bool
}

func newWalletSet() *walletSet {
	return &walletSet{seen: map[uuid.UUID]bool{}}
}

func (w *walletSet) add(id uuid.UUID) {
	if id == uuid.Nil || w.seen[id] {
		return
	}
	w.seen[id] = true
	w.ids = append(w.ids, id)
}
