package ramps_test

import (
	"context"
	"errors"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// Settling money already paid is not gated on eligibility, or a provider switched
// off during an incident would strand the crypto in its custody.
func TestDeliverOnrampSettlesAfterTheProviderWasSwitchedOff(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	h.config(models.ProviderOzav, false, models.ProviderModeSandbox)
	tx := settledOnramp()
	h.delivering.order = &ramp.RampOrder{ProviderOrderID: "ozav_tr_delivery_1", TxHash: "5xDeliverySig"}
	h.txRepo.EXPECT().Update(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	require.NoError(t, h.onramp.Deliver(context.Background(), tx))
	require.NotNil(t, h.delivering.delivered)
	require.Equal(t, "5xDeliverySig", *tx.DepositTxSignature)
}

// The next webhook or sweep must try again rather than treat the order as done.
func TestDeliverOnrampLeavesTheOrderUndeliveredWhenItFails(t *testing.T) {
	cases := map[string]struct {
		prepare func(*harness, *models.Transaction)
		sent    bool
	}{
		"no wallet to deliver to": {prepare: func(_ *harness, tx *models.Transaction) { tx.DestinationWallet = "" }},
		"no customer to name": {prepare: func(h *harness, _ *models.Transaction) {
			h.integ.ExpectedCalls = nil
			h.integ.EXPECT().FindByCustomerAndProvider(userID, models.ProviderOzav).Return(nil, nil).Once()
		}},
		"provider refuses the send": {prepare: func(h *harness, _ *models.Transaction) {
			h.delivering.err = errors.New("ozav: POST /transfers: 422")
		}, sent: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
			tx := settledOnramp()
			c.prepare(h, tx)

			require.Error(t, h.onramp.Deliver(context.Background(), tx))
			require.Equal(t, c.sent, h.delivering.delivered != nil)
			require.Nil(t, tx.DeliveryOrderID)
			h.txRepo.AssertNotCalled(t, "Update", testifymock.Anything)
		})
	}
}

// The webhook, its redelivery and the sweep reach the same row: a retry after a
// send whose bookkeeping was lost must be absorbed upstream, not paid twice.
func TestDeliverOnrampPresentsTheSameKeyOnEveryAttempt(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	h.delivering.order = &ramp.RampOrder{ProviderOrderID: "ozav_tr_delivery_1"}
	lost := errors.New("pq: connection reset")
	h.txRepo.EXPECT().Update(testifymock.Anything).Return(lost).Twice()

	var keys []string
	for range 2 {
		require.ErrorIs(t, h.onramp.Deliver(context.Background(), settledOnramp()), lost)
		keys = append(keys, h.delivering.delivered.IdempotencyKey)
	}
	require.Equal(t, keys[0], keys[1])
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

// A send the provider created and refused at once must not park the order on
// that dead transfer: the attempt is counted so the retry carries a new key.
func TestDeliverOnrampCountsARefusedSendAndRotatesTheKey(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	h.delivering.order = &ramp.RampOrder{ProviderOrderID: "ozav_tr_dead", DeliveryFailed: true, FailureReason: "insufficient balance"}
	tx := settledOnramp()
	h.txRepo.EXPECT().Update(tx).Return(nil).Once()

	require.Error(t, h.onramp.Deliver(context.Background(), tx))
	firstKey := h.delivering.delivered.IdempotencyKey
	require.Nil(t, tx.DeliveryOrderID)
	require.Equal(t, 1, tx.DeliveryAttempts)

	h.txRepo.EXPECT().Update(tx).Return(nil).Once()
	require.Error(t, h.onramp.Deliver(context.Background(), tx))
	require.NotEqual(t, firstKey, h.delivering.delivered.IdempotencyKey)
}

// A refused send whose count could not be written is reported with both
// failures: left uncounted, the next attempt would repeat the refused key.
func TestDeliverOnrampReportsARefusalItCouldNotRecord(t *testing.T) {
	cases := map[string]func(*harness){
		"refused at once": func(h *harness) {
			h.delivering.order = &ramp.RampOrder{ProviderOrderID: "ozav_tr_dead", DeliveryFailed: true, FailureReason: "insufficient balance"}
		},
		"key replayed": func(h *harness) { h.delivering.err = ramp.ErrIdempotencyConflict },
	}
	for name, prepare := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
			prepare(h)
			tx := settledOnramp()
			lost := errors.New("pq: connection reset")
			h.txRepo.EXPECT().Update(tx).Return(lost).Once()

			err := h.onramp.Deliver(context.Background(), tx)

			require.ErrorIs(t, err, lost)
			require.ErrorContains(t, err, "deliver onramp")
			h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
		})
	}
}
