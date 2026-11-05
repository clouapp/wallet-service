package ramps

import (
	"context"
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// canceledBeforeDepositReason is what the row says once the app withdrew an
// order its wallet could not fund. It is for the operator; the app shows its
// own sentence.
const canceledBeforeDepositReason = "canceled before deposit: the wallet could not fund it"

// Cancel withdraws one of the customer's off-ramp orders nobody has funded.
//
// The app learns the deposit address only from the created order, and only
// then can it tell whether the wallet pays for that deposit (the first one to a
// Bridge customer's address also pays the rent of the address's token account).
// When it cannot, the app cancels here instead of leaving an order in
// `awaiting_funds` that no one will ever fund.
//
// Deliberately NOT ResolveProvider: withdrawing an order moves no money, and
// gating it on eligibility would leave it open upstream the moment a KYC
// lapsed or an operator switched the provider off.
//
// The provider refuses once the deposit is in, and an order a deposit has
// claimed by hash is refused here too: its money is on the way to the managed
// wallet. It runs under the customer's settle lock, so a settlement cannot pay
// out an order this is withdrawing, and the write is a CAS on an unclaimed
// `awaiting_funds`: if the provider's own transfer.updated event or a deposit
// claim got there first, the row it wrote is the answer.
func (s *OfframpService) Cancel(ctx context.Context, customerID uint, externalID string) (*OrderView, error) {
	var view *OrderView
	err := s.locker.WithLock(ctx, settleLockKey(customerID), func(ctx context.Context) error {
		var err error
		view, err = s.cancelLocked(ctx, customerID, externalID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

func (s *OfframpService) cancelLocked(ctx context.Context, customerID uint, externalID string) (*OrderView, error) {
	tx, err := s.txRepo.FindByCustomerAndExternalID(customerID, externalID)
	if err != nil {
		return nil, fmt.Errorf("ramps: load offramp: %w", err)
	}
	if tx == nil || tx.Type != models.TransactionTypeOfframp {
		return nil, ErrOrderNotFound
	}
	if tx.Status != models.TxStatusAwaitingFunds {
		return nil, fmt.Errorf("ramps: cancel offramp %s in %s: %w", tx.ExternalID, tx.Status, ErrInvalidOrderState)
	}
	if depositClaimed(tx) {
		return nil, fmt.Errorf("ramps: cancel offramp %s with a claimed deposit: %w", tx.ExternalID, ErrInvalidOrderState)
	}

	adapter, ref, err := s.router.ResolveForSettlement(ctx, tx.CustomerID, tx.Provider)
	if err != nil {
		return nil, fmt.Errorf("ramps: resolve provider to cancel offramp %s: %w", tx.ExternalID, err)
	}
	canceler, ok := adapter.(ramp.OfframpCanceler)
	if !ok {
		return nil, fmt.Errorf("ramps: %s cannot cancel offramp %s: %w", tx.Provider, tx.ExternalID, ramp.ErrProviderNotImplemented)
	}
	if err := canceler.CancelOfframp(ctx, ref, tx.GatewayOrderID); err != nil {
		return nil, fmt.Errorf("ramps: cancel offramp %s: %w", tx.ExternalID, err)
	}

	updated, err := s.txRepo.CancelUnfundedOfframp(tx, canceledBeforeDepositReason)
	if err != nil {
		return nil, fmt.Errorf("ramps: persist canceled offramp %s: %w", tx.ExternalID, err)
	}
	if !updated {
		facades.Log().Debugf("ramps: cancel of %s lost the write to the provider's event", tx.ExternalID)
		reloaded, err := s.txRepo.FindByCustomerAndExternalID(customerID, externalID)
		if err != nil {
			return nil, fmt.Errorf("ramps: reload canceled offramp %s: %w", tx.ExternalID, err)
		}
		if reloaded == nil {
			return nil, ErrOrderNotFound
		}
		return s.toOrderView(reloaded), nil
	}
	s.publish(tx)
	return s.toOrderView(tx), nil
}
