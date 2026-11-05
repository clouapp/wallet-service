package ramps

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
)

// Deliver finishes an on-ramp whose pay-in settled but whose crypto is still in
// the provider's custody.
//
// It exists because one provider's on-ramp buys the crypto without delivering
// it. Ozav accepts `fiat_payment -> wallet` and refuses
// `fiat_payment -> crypto_address`, so the tokens land in the customer's wallet
// at the provider and a second, separate send puts them where the user can
// spend them. Bridge has no such gap — its account was provisioned against the
// user's address — which is why the capability is an optional port rather than
// a method on ProviderAdapter.
//
// Every guard here is about not paying the user twice. The send is
// irreversible, and several things reach this function for the same order:
// the webhook that reports the pay-in settled and its redeliveries, a read that
// re-checked the provider and saw it first, the admin's refresh, and the
// reconciler's sweep (rampsvc.DeliverSettledOnramp lists them). DeliveryOrderID
// is what makes every ask after the first a no-op, and the derived idempotency
// key is what makes two that race, or a retry that crashed after sending, safe
// upstream.
//
// A provider with nothing to deliver is not an error: the order is already
// finished, and reporting a failure would put a completed order into a retry
// loop.
//
// It lives on the engine every client ramp service embeds, so the read path's
// refresh can ask for it too (see refreshInFlight); the on-ramp service is what
// the webhook processor and the reconciler hold it through.
func (e *engine) Deliver(ctx context.Context, tx *models.Transaction) error {
	// Nothing is in custody until the pay-in settles, so there is nothing to
	// forward; and once the send is placed it is not placed again. The paths
	// that report any other state reach here too.
	if tx == nil {
		return nil
	}
	if !rampsvc.PayInReadyForDelivery(tx) && !tx.AwaitsDelivery() {
		return nil
	}
	if tx.DestinationWallet == "" {
		return fmt.Errorf("ramps: onramp %s has no destination wallet to deliver to", tx.ExternalID)
	}
	// Every path reaches here, not only the sweep that filters on it, so the
	// ceiling is enforced where the send is placed.
	if tx.DeliveryAttempts >= e.config.OzavDeliveryMaxAttempts() {
		return nil
	}

	// Deliberately NOT ResolveProvider: this is settlement of money the user
	// already paid, and gating it on eligibility would strand the crypto in the
	// provider's custody the moment an unrelated state changed — a customer
	// suspended after paying, a provider disabled during an incident. Every
	// retry would refuse identically, forever.
	adapter, ref, err := e.router.ResolveForSettlement(ctx, tx.CustomerID, tx.Provider)
	if err != nil {
		return fmt.Errorf("ramps: resolve provider to deliver onramp %s: %w", tx.ExternalID, err)
	}
	deliverer, ok := adapter.(ramp.OnrampDeliveryAdapter)
	if !ok {
		// The provider delivered as part of the pay-in. Nothing left to do.
		return nil
	}

	order, err := deliverer.DeliverOnramp(ctx, ref, ramp.OnrampDeliveryRequest{
		// The amount the pay-in actually produced, read off the row the
		// provider's own answer wrote — never the quote's estimate, which is
		// what the user was shown before the rate settled.
		Amount: tx.AmountToken,
		Destination: ramp.CryptoLeg{
			Currency:    tx.TargetToken,
			PaymentRail: deref(tx.Chain),
			Address:     tx.DestinationWallet,
		},
		AttestedOwnershipAt: attestedOwnershipAt(tx),
		IdempotencyKey:      deliveryIdempotencyKey(tx),
	})
	if err != nil {
		// A key the provider already settled on is replayed, whatever the
		// state now: count the attempt so the next one carries a new key
		// instead of repeating the same refusal forever.
		err = fmt.Errorf("ramps: deliver onramp %s: %w", tx.ExternalID, err)
		if errors.Is(err, ramp.ErrIdempotencyConflict) {
			return errors.Join(err, e.recordDeliveryFailure(ctx, tx, "", err.Error()))
		}
		return err
	}
	// The send was created and refused at once (an insufficient balance, say).
	// Keeping its id would park the order on a dead transfer and repeat its key.
	if order.DeliveryFailed {
		return errors.Join(
			fmt.Errorf("ramps: deliver onramp %s: delivery %s failed: %s",
				tx.ExternalID, order.ProviderOrderID, order.FailureReason),
			e.recordDeliveryFailure(ctx, tx, order.ProviderOrderID, order.FailureReason))
	}

	tx.DeliveryOrderID = &order.ProviderOrderID
	// The delivery is the movement that actually reaches the chain, so its hash
	// is the one worth keeping on the order.
	if order.TxHash != "" {
		tx.DepositTxSignature = &order.TxHash
	}
	if err := e.txRepo.Update(tx); err != nil {
		// The send already happened. Losing the id here means the next sweep
		// would try again — which the provider's idempotency key absorbs, and
		// which is why that key is derived from the order rather than generated.
		return fmt.Errorf("ramps: persist onramp delivery %s: %w", tx.ExternalID, err)
	}
	e.publish(tx)
	return nil
}

// recordDeliveryFailure counts a refused send on the row, which is what makes
// the next attempt use a new idempotency key (see deliveryIdempotencyKey). A
// write that fails is returned: the attempt went uncounted, and the next one
// would repeat the refused key.
func (e *engine) recordDeliveryFailure(ctx context.Context, tx *models.Transaction, deliveryID, reason string) error {
	out := rampsvc.DeliveryOutcome{ProviderOrderID: deliveryID, Failed: true, FailureReason: reason}
	maxAttempts := e.config.OzavDeliveryMaxAttempts()
	if !rampsvc.ApplyDeliveryOutcome(tx, out, maxAttempts) {
		return nil
	}
	if err := e.txRepo.Update(tx); err != nil {
		return fmt.Errorf("ramps: persist onramp %s delivery failure: %w", tx.ExternalID, err)
	}
	rampsvc.LogDeliveryFailure(ctx, tx, out, maxAttempts)
	return nil
}

// attestedOwnershipAt is when the user asserted the destination is theirs,
// which is when they placed the order naming it. Resolution 520 asks for that
// moment, not for the moment the send happens.
func attestedOwnershipAt(tx *models.Transaction) time.Time {
	if tx.CreatedAt == nil {
		return time.Now().UTC()
	}
	return tx.CreatedAt.StdTime()
}

// deliveryIdempotencyKey is derived, never random: the whole point is that the
// sweep, the webhook and a retry all present the same key for the same order.
// It is distinct from the pay-in's key because it is a different movement —
// reusing that one would collide against the transfer already created.
//
// The first attempt keeps the key earlier releases derived, so a send placed
// before a deploy whose id was never saved is replayed rather than repeated.
// Each retry after a reported failure gets its own, because the provider
// replays the stored failure for a key it has already seen.
func deliveryIdempotencyKey(tx *models.Transaction) string {
	if tx.DeliveryAttempts == 0 {
		return "onramp-deliver-" + strings.ReplaceAll(tx.ExternalID, "-", "")
	}
	return fmt.Sprintf("onramp-delivery:%s:%d", tx.ExternalID, tx.DeliveryAttempts)
}
