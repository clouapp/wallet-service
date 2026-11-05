package ramp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// DeliveryOutcome is what the provider reports about an on-ramp's second
// movement, whichever path read it: the delivery transfer's own webhook, a
// read that re-checked it, or the reconciler's sweep.
type DeliveryOutcome struct {
	ProviderOrderID string
	Status          string
	TxHash          string
	Failed          bool
	FailureReason   string
	OccurredAt      *time.Time
}

// DeliveryOutcomeFromOrder reads a re-fetched delivery transfer.
func DeliveryOutcomeFromOrder(order *ramp.RampOrder) DeliveryOutcome {
	if order == nil {
		return DeliveryOutcome{}
	}
	return DeliveryOutcome{
		ProviderOrderID: order.ProviderOrderID,
		Status:          order.Status,
		TxHash:          order.TxHash,
		Failed:          order.DeliveryFailed,
		FailureReason:   order.FailureReason,
	}
}

// ApplyDeliveryOutcome records the outcome of an on-ramp's delivery on the
// row and reports whether anything changed. It is the one place that decides
// what a delivery's result means, so the three paths that can see it agree.
//
// The pay-in's status is left alone unless the delivery completed: the user
// paid and the provider bought the crypto, and overwriting that with the
// delivery's status would say the money never arrived when what failed is the
// leg that forwards it. Nor are the amounts or the gateway id touched — they
// belong to the pay-in transfer.
//
// A failure clears the delivery id and counts the attempt, which is what makes
// the order due again for the sweep (FindUndeliveredOnramps) with the next
// attempt's idempotency key. A retry trusts the provider's terminal "failed":
// the transfer it names did not move the tokens. Attempts stop at maxAttempts,
// after which the order is left for an operator with the crypto still in the
// provider's custody.
func ApplyDeliveryOutcome(tx *models.Transaction, out DeliveryOutcome, maxAttempts int) bool {
	if tx == nil || OnrampCredited(tx) {
		return false
	}
	if out.Failed {
		reason := "delivery to the user's wallet failed"
		if out.FailureReason != "" {
			reason += ": " + out.FailureReason
		}
		tx.FailureReason = &reason
		tx.DeliveryOrderID = nil
		tx.DeliveryAttempts++
		return true
	}
	completed := out.Status == models.TxStatusCompleted
	if out.TxHash == "" && !completed {
		return false
	}
	changed := false
	if out.TxHash != "" {
		tx.DepositTxSignature = &out.TxHash
		changed = true
	}
	// The hash may never arrive (see onrampDeliveryStatus), so the delivery's
	// completion is what closes the order; a row already closed is not rewritten.
	if completed && tx.Status != models.TxStatusCompleted {
		tx.Status = models.TxStatusCompleted
		if tx.CompletedAt == nil {
			now := time.Now()
			if out.OccurredAt != nil {
				now = *out.OccurredAt
			}
			tx.CompletedAt = &now
		}
		// A retry that delivered leaves no failure behind: the reason of an
		// earlier attempt would read as this order's outcome.
		tx.FailureReason = nil
		changed = true
	}
	return changed
}

// LogDeliveryFailure reports a failed delivery once the row recording it is
// saved. It is the caller's to run, after the write: ApplyDeliveryOutcome only
// mutates, and a log there would announce attempts a lost write never counted.
func LogDeliveryFailure(ctx context.Context, tx *models.Transaction, out DeliveryOutcome, maxAttempts int) {
	if !out.Failed {
		return
	}
	log := facades.Log().WithContext(ctx)
	if tx.DeliveryAttempts >= maxAttempts {
		log.Errorf(
			"ramp: onramp %s delivery exhausted %d attempts — the crypto is still in the provider's custody",
			tx.ExternalID, tx.DeliveryAttempts)
		return
	}
	log.Warningf(
		"ramp: onramp %s delivery %s failed — will retry (%d/%d)",
		tx.ExternalID, out.ProviderOrderID, tx.DeliveryAttempts, maxAttempts)
}

// RefreshDelivery re-reads an on-ramp's in-flight delivery transfer and
// applies its outcome, for when the delivery's own webhook was lost.
//
// The write is conditional on the delivery id the read was made for, so a
// webhook that recorded the same outcome first is not counted twice; a lost
// race reports false and leaves tx as this call mutated it, for the caller to
// reload if it answers with the row.
func RefreshDelivery(
	ctx context.Context,
	adapter ramp.ProviderAdapter,
	ref ramp.CustomerRef,
	txRepo repositories.TransactionRepository,
	tx *models.Transaction,
	maxAttempts int,
) (bool, error) {
	deliveryID := ""
	if tx.DeliveryOrderID != nil {
		deliveryID = strings.TrimSpace(*tx.DeliveryOrderID)
	}
	if deliveryID == "" || OnrampCredited(tx) {
		return false, nil
	}
	order, err := adapter.GetOrder(ctx, ref, deliveryID)
	if err != nil {
		return false, fmt.Errorf("ramp: read onramp %s delivery: %w", tx.ExternalID, err)
	}
	out := DeliveryOutcomeFromOrder(order)
	if !ApplyDeliveryOutcome(tx, out, maxAttempts) {
		return false, nil
	}
	saved, err := txRepo.SaveDeliveryOutcome(tx, deliveryID)
	if err != nil {
		return false, fmt.Errorf("ramp: persist onramp %s delivery outcome: %w", tx.ExternalID, err)
	}
	if saved {
		LogDeliveryFailure(ctx, tx, out, maxAttempts)
	}
	return saved, nil
}
