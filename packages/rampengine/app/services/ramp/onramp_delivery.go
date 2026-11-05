package ramp

import (
	"context"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// OnrampDeliverer finishes an on-ramp whose pay-in settled but whose crypto is
// still in the provider's custody.
//
// The processor holds it as an interface rather than importing the client ramp
// service, and the split is the honest one: knowing *that* a pay-in settled is
// the job of whoever saw it, because more than one thing can; knowing *what*
// finishing the order takes belongs to the service that placed it. A nil
// deliverer means nothing is asked, which is what every caller that does not
// serve this rail wants.
type OnrampDeliverer interface {
	Deliver(ctx context.Context, tx *models.Transaction) error
}

// DeliverSettledOnramp asks for the second hop of an on-ramp that awaits it.
//
// Every path that can see an on-ramp complete calls it, because whichever sees
// it first is the one that has to ask: the provider's event (WebhookProcessor),
// a read that re-checks the provider (the client's refreshInFlight), the
// admin's refresh (Reconciler.RefreshOne). The sweep's own retry,
// sweepUndelivered, reaches the same Deliver through FindUndeliveredOnramps.
// The send happens once however many of them ask: delivery_order_id
// (models.Transaction.AwaitsDelivery) turns the later asks into no-ops, and the
// idempotency key the service derives from the order absorbs two that race.
//
// Call it after the row that says completed is committed: the send is real
// money and must not go out ahead of the record of the pay-in behind it.
//
// A failure is logged and not returned. The pay-in did settle and the row says
// so; failing the caller would roll back or refuse a status that is right,
// while the send is retried by the next path that sees the order — a late
// event, the sweep.
func DeliverSettledOnramp(ctx context.Context, deliverer OnrampDeliverer, tx *models.Transaction) {
	if deliverer == nil || tx == nil {
		return
	}
	if !PayInReadyForDelivery(tx) && !tx.AwaitsDelivery() {
		return
	}
	if err := deliverer.Deliver(ctx, tx); err != nil {
		facades.Log().Errorf("ramp: deliver onramp %s: %v", tx.ExternalID, err)
	}
}
