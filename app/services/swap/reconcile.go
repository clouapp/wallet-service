package swap

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/security"
)

// reconcileBatch bounds one pass: the oldest-checked open orders first.
const reconcileBatch = 50

// OpenOrders lists accepted orders that are not terminal yet.
type OpenOrders interface {
	ListOpenOrders(context.Context, int) ([]models.SwapOrder, error)
}

// Accounts loads the account an order belongs to.
type Accounts interface {
	FindByID(context.Context, uuid.UUID) (*models.Account, error)
}

// ReconcileSwaps advances the open orders no caller is polling: funding bytes
// whose send was never answered are sent again (never re-signed), a funding that
// failed on chain ends the order, and provider outcomes are verified on chain
// exactly as a caller's GET would. Errors of one order never stop the pass.
func (s *Service) ReconcileSwaps(ctx context.Context) error {
	open, ok := s.deps.Store.(OpenOrders)
	if !ok || s.deps.Accounts == nil || s.deps.Wallets == nil {
		return nil
	}
	orders, err := open.ListOpenOrders(ctx, reconcileBatch)
	if err != nil {
		return err
	}
	for i := range orders {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		order := orders[i]
		account, err := s.deps.Accounts.FindByID(ctx, order.AccountID)
		if err != nil {
			slog.Warn("swap reconcile: account unread", "order_id", order.ID, "error", security.RedactError(err))
			continue
		}
		source, err := s.deps.Wallets.FindByID(ctx, order.SourceWalletID)
		if err != nil {
			slog.Warn("swap reconcile: source wallet unread", "order_id", order.ID, "error", security.RedactError(err))
			continue
		}
		if _, err := s.Refresh(ctx, account, source, order.ID); err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("swap reconcile: order not advanced", "order_id", order.ID, "status", order.Status, "error", security.RedactError(err))
		}
	}
	return nil
}
