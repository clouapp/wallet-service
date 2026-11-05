package ramp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// A delivery that lands after a failed attempt closes the order clean: the
// earlier attempt's reason would read as the outcome of a completed order.
func TestApplyDeliveryOutcomeClearsTheReasonOfAnEarlierAttempt(t *testing.T) {
	reason := "delivery to the user's wallet failed: travel_rule_rejected"
	tx := &models.Transaction{
		Type:             models.TransactionTypeOnramp,
		Status:           models.TxStatusProcessing,
		DeliveryAttempts: 1,
		FailureReason:    &reason,
	}

	changed := ApplyDeliveryOutcome(tx, DeliveryOutcome{Status: models.TxStatusCompleted, TxHash: "5Sig"}, 3)

	require.True(t, changed)
	require.Equal(t, models.TxStatusCompleted, tx.Status)
	require.Nil(t, tx.FailureReason)
	require.Equal(t, 1, tx.DeliveryAttempts, "the attempt count stays as history")
}

// What each outcome does to a row still waiting on its delivery.
func TestApplyDeliveryOutcome(t *testing.T) {
	cases := []struct {
		name         string
		attempts     int
		out          DeliveryOutcome
		wantChanged  bool
		wantStatus   string
		wantReason   string
		wantAttempts int
		wantHash     string
		wantDelivery bool
	}{
		{
			name: "failed with the provider's reason", attempts: 0,
			out:         DeliveryOutcome{Failed: true, FailureReason: "insufficient gas"},
			wantChanged: true, wantStatus: models.TxStatusProcessing,
			wantReason: "delivery to the user's wallet failed: insufficient gas", wantAttempts: 1,
		},
		{
			name: "failed without a reason", attempts: 0,
			out:         DeliveryOutcome{Failed: true},
			wantChanged: true, wantStatus: models.TxStatusProcessing,
			wantReason: "delivery to the user's wallet failed", wantAttempts: 1,
		},
		{
			// The count reaches the ceiling and stops there: the sweep's
			// attempts filter is what leaves it to an operator.
			name: "the failure that reaches the ceiling", attempts: 2,
			out:         DeliveryOutcome{Failed: true},
			wantChanged: true, wantStatus: models.TxStatusProcessing,
			wantReason: "delivery to the user's wallet failed", wantAttempts: 3,
		},
		{
			name: "a hash without completion is kept, the status is not touched", attempts: 0,
			out:         DeliveryOutcome{Status: models.TxStatusProcessing, TxHash: "5Sig"},
			wantChanged: true, wantStatus: models.TxStatusProcessing, wantHash: "5Sig", wantDelivery: true,
		},
		{
			name: "neither hash nor completion is no news", attempts: 0,
			out:         DeliveryOutcome{Status: models.TxStatusProcessing},
			wantChanged: false, wantStatus: models.TxStatusProcessing, wantDelivery: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			delivery := "ozav_tr_delivery"
			tx := &models.Transaction{
				Type: models.TransactionTypeOnramp, Status: models.TxStatusProcessing,
				DeliveryAttempts: c.attempts, DeliveryOrderID: &delivery,
			}

			changed := ApplyDeliveryOutcome(tx, c.out, 3)

			require.Equal(t, c.wantChanged, changed)
			require.Equal(t, c.wantStatus, tx.Status)
			require.Equal(t, c.wantAttempts, tx.DeliveryAttempts)
			if c.wantReason == "" {
				require.Nil(t, tx.FailureReason)
			} else {
				require.Equal(t, c.wantReason, *tx.FailureReason)
			}
			if c.wantHash == "" {
				require.Nil(t, tx.DepositTxSignature)
			} else {
				require.Equal(t, c.wantHash, *tx.DepositTxSignature)
			}
			// A failure frees the order for the next attempt.
			require.Equal(t, c.wantDelivery, tx.DeliveryOrderID != nil)
		})
	}
}

// When the provider says when the delivery completed, that is the order's
// completion time, not the moment this path happened to read it.
func TestApplyDeliveryOutcomeCompletesAtTheProvidersTime(t *testing.T) {
	occurred := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	tx := &models.Transaction{Type: models.TransactionTypeOnramp, Status: models.TxStatusProcessing}

	changed := ApplyDeliveryOutcome(tx, DeliveryOutcome{Status: models.TxStatusCompleted, OccurredAt: &occurred}, 3)

	require.True(t, changed)
	require.Equal(t, models.TxStatusCompleted, tx.Status)
	require.Equal(t, occurred, *tx.CompletedAt)
	require.Nil(t, tx.DepositTxSignature, "completion closes the order without a hash")
}

// An order whose tokens already reached the user is never rewritten, whatever
// a late or replayed outcome says.
func TestApplyDeliveryOutcomeLeavesACreditedOrderAlone(t *testing.T) {
	sig := "5Sig"
	tx := &models.Transaction{
		Type: models.TransactionTypeOnramp, Status: models.TxStatusCompleted, DepositTxSignature: &sig,
	}

	changed := ApplyDeliveryOutcome(tx, DeliveryOutcome{Failed: true, FailureReason: "late"}, 3)

	require.False(t, changed)
	require.Nil(t, tx.FailureReason)
	require.Zero(t, tx.DeliveryAttempts)
}
