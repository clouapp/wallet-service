package ramps

import (
	"context"
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// fundingRefreshWindow is how long a funding action must still live for a
// refresh to hand it back unchanged: the wallet needs time to sign it.
const fundingRefreshWindow = 15 * time.Second

// fundingLockKey is the lock that serializes everything that rewrites one
// order's funding authorization.
func fundingLockKey(txID uint) string {
	return fmt.Sprintf("funding:%d", txID)
}

// RefreshFundingAction answers the customer's bank payout with a funding
// action the wallet can still sign (spec §5.7, plan Task 7). The action a
// provider prepares lives for a short while; the quote it was prepared from
// lives longer, and the order is fundable for as long as the quote.
//
//   - Anything but a bank payout awaiting its funds with a prepared
//     authorization is ErrFundingActionExpired.
//   - A quote that lapsed fails the authorization and expires the order.
//   - An action with time left is returned as it is, with no provider call.
//   - Otherwise the provider prepares again and the action is replaced, under
//     a compare-and-swap on its revision: the loser answers the winner's.
//
// Like Cancel, it resolves the order's own provider without the eligibility
// gates: preparing moves no money, and an order that exists finishes.
func (s *OfframpService) RefreshFundingAction(
	ctx context.Context, _ *models.User, customer *models.Customer, orderID string,
) (*OrderView, error) {
	tx, err := s.txRepo.FindByCustomerAndExternalID(customer.ID, orderID)
	if err != nil {
		return nil, fmt.Errorf("ramps: load offramp: %w", err)
	}
	if tx == nil || tx.Type != models.TransactionTypeOfframp {
		return nil, ErrOrderNotFound
	}
	if s.funding == nil {
		return nil, fmt.Errorf("ramps: funding refresh not wired: %w", ramp.ErrOfframpCreateUnavailable)
	}
	var view *OrderView
	err = s.locker.WithLock(ctx, fundingLockKey(tx.ID), func(ctx context.Context) error {
		var err error
		view, err = s.refreshLocked(ctx, customer, orderID)
		return err
	})
	return view, err
}

// refreshLocked is RefreshFundingAction under the order's funding lock, which
// reads the order again: what it saw before the lock may have moved.
func (s *OfframpService) refreshLocked(ctx context.Context, customer *models.Customer, orderID string) (*OrderView, error) {
	tx, err := s.txRepo.FindByCustomerAndExternalID(customer.ID, orderID)
	if err != nil {
		return nil, fmt.Errorf("ramps: reload offramp: %w", err)
	}
	if tx == nil {
		return nil, ErrOrderNotFound
	}
	if tx.Type != models.TransactionTypeOfframp || tx.Status != models.TxStatusAwaitingFunds ||
		!tx.FundedByAuthorization() || tx.EstimateID == nil {
		return nil, ramp.ErrFundingActionExpired
	}
	auth, err := s.funding.FindByTransaction(tx.ID)
	if err != nil {
		return nil, fmt.Errorf("ramps: load funding authorization: %w", err)
	}
	if auth == nil || auth.State != models.FundingAuthPrepared {
		return nil, ramp.ErrFundingActionExpired
	}
	estimate, err := s.estimateRepo.FindByCustomerAndID(customer.ID, *tx.EstimateID)
	if err != nil {
		return nil, fmt.Errorf("ramps: load estimate of offramp %s: %w", tx.ExternalID, err)
	}
	if estimate == nil {
		return nil, fmt.Errorf("ramps: estimate of offramp %s is missing", tx.ExternalID)
	}
	snapshot, ok := decodeEstimateSnapshot(estimate.RawResponse)
	if !ok {
		return nil, fmt.Errorf("ramps: decode estimate snapshot of offramp %s", tx.ExternalID)
	}

	now := time.Now()
	if !deref(estimate.ExpiresAt).After(now) {
		changed, err := s.funding.FailAndExpire(auth, models.FundingFailureQuoteExpired)
		if err != nil {
			return nil, fmt.Errorf("ramps: expire offramp %s: %w", tx.ExternalID, err)
		}
		if changed {
			tx.Status = models.TxStatusExpired
			s.publish(tx)
		}
		return nil, ramp.ErrFundingActionExpired
	}
	if auth.ExpiresAt.After(now.Add(fundingRefreshWindow)) {
		return s.toOrderView(tx), nil
	}
	return s.replaceAction(ctx, tx, auth, estimate, snapshot)
}

// replaceAction prepares the order's funding again and swaps the action in,
// and answers the order reloaded. A swap lost to another writer answers what
// that writer left.
func (s *OfframpService) replaceAction(
	ctx context.Context, tx *models.Transaction, auth *models.OfframpFundingAuthorization,
	estimate *models.RampEstimate, snapshot *estimateSnapshot,
) (*OrderView, error) {
	adapter, ref, err := s.router.ResolveForSettlement(ctx, tx.CustomerID, tx.Provider)
	if err != nil {
		return nil, fmt.Errorf("ramps: resolve provider to refresh offramp %s: %w", tx.ExternalID, err)
	}
	preparer, ok := adapter.(ramp.OfframpPrepareAdapter)
	if !ok {
		return nil, fmt.Errorf("ramps: %s prepares no offramp funding: %w", tx.Provider, ramp.ErrProviderNotImplemented)
	}
	prepared, err := preparer.PrepareOfframp(ctx, ref, prepareRequest(estimate, snapshot))
	if err == nil {
		err = checkPrepared(prepared)
	}
	if err != nil {
		return nil, fmt.Errorf("ramps: prepare offramp funding again: %w", err)
	}
	state, payload, err := sealPrepared(prepared)
	if err != nil {
		return nil, err
	}
	previous := auth.ActionRevision
	auth.ProviderStateCiphertext, auth.ActionPayloadCiphertext = state, payload
	auth.UnsignedActionFingerprint, auth.ExpiresAt = prepared.MessageFingerprint, prepared.Action.ExpiresAt
	if _, err := s.funding.ReplaceAction(auth, previous); err != nil {
		return nil, fmt.Errorf("ramps: replace funding action of offramp %s: %w", tx.ExternalID, err)
	}
	// Swapped or not, the answer is the row as it stands now: tx was read
	// before the prepare, up to a provider timeout ago, and the order may
	// have moved since (the reconciler expired it, another writer won).
	current, err := s.txRepo.FindByCustomerAndExternalID(tx.CustomerID, tx.ExternalID)
	if err != nil {
		return nil, fmt.Errorf("ramps: reload offramp %s: %w", tx.ExternalID, err)
	}
	if current == nil {
		return nil, ErrOrderNotFound
	}
	return s.toOrderView(current), nil
}
