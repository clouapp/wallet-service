package ramp_test

import (
	"context"
	"errors"
	"testing"
	"time"

	mockslog "github.com/goravel/framework/mocks/log"
	"github.com/goravel/framework/testing/mock"
	"github.com/stretchr/testify/assert"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/webhooks"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

const receivedBody = `{"type":"transfer.updated"}`

type receiverHarness struct {
	receiver   *rampsvc.WebhookReceiver
	adapter    *mocks.MockProviderAdapter
	events     *mocks.MockWebhookEventRepository
	dispatcher *mocks.MockEventDispatcher
}

func newReceiverHarness(t *testing.T) *receiverHarness {
	t.Helper()
	h := &receiverHarness{
		adapter:    mocks.NewMockProviderAdapter(t),
		events:     mocks.NewMockWebhookEventRepository(t),
		dispatcher: mocks.NewMockEventDispatcher(t),
	}
	h.adapter.EXPECT().Code().Return(models.ProviderBridge).Maybe()
	h.receiver = rampsvc.NewWebhookReceiver(ramp.NewFactory(h.adapter), h.events, h.dispatcher)
	return h
}

func delivery() dto.ReceiveWebhookDTO {
	return dto.ReceiveWebhookDTO{
		Provider: models.ProviderBridge,
		Headers:  map[string]string{"X-Webhook-Signature": "t=1,v0=sig"},
		Body:     []byte(receivedBody),
	}
}

// verifies makes the adapter accept the delivery and answer result.
func (h *receiverHarness) verifies(ctx context.Context, result *ramp.WebhookResult) {
	in := delivery()
	h.adapter.EXPECT().ParseWebhook(ctx, in.Headers, in.Body).Return(result, nil).Once()
}

// stores accepts the insert, assigns id to the row as the database would, and
// hands the row back through stored so the test can read what was built.
func (h *receiverHarness) stores(id uint, inserted bool, stored **models.WebhookEvent) {
	h.events.EXPECT().CreateIfNew(testifymock.Anything).
		RunAndReturn(func(row *models.WebhookEvent) (bool, error) {
			if inserted {
				row.ID = id
			}
			*stored = row
			return inserted, nil
		}).Once()
}

func TestWebhookReceiver_Receive_UnknownProviderIsRejected(t *testing.T) {
	h := newReceiverHarness(t)
	in := delivery()
	in.Provider = "nobody"

	err := h.receiver.Receive(context.Background(), in)

	require.ErrorIs(t, err, rampsvc.ErrWebhookRejected)
	assert.ErrorIs(t, err, ramp.ErrProviderNotImplemented)
}

func TestWebhookReceiver_Receive_UnverifiableDeliveryIsRejectedBeforeAnyWrite(t *testing.T) {
	h := newReceiverHarness(t)
	ctx := context.Background()
	in := delivery()
	cause := errors.New("bridge: signature mismatch")
	h.adapter.EXPECT().ParseWebhook(ctx, in.Headers, in.Body).Return(nil, cause).Once()

	err := h.receiver.Receive(ctx, in)

	require.ErrorIs(t, err, rampsvc.ErrWebhookRejected)
	assert.ErrorIs(t, err, cause)
}

func TestWebhookReceiver_Receive_StoresTheVerifiedEventAndDispatchesIt(t *testing.T) {
	h := newReceiverHarness(t)
	ctx := context.Background()
	h.verifies(ctx, &ramp.WebhookResult{
		EventID: "evt_1", EventType: "transfer.updated",
		ProviderOrderID: "trf_1", ExternalCustomerID: "cus_1",
	})
	var stored *models.WebhookEvent
	h.stores(42, true, &stored)
	h.dispatcher.EXPECT().Dispatch(uint(42)).Return(nil).Once()

	require.NoError(t, h.receiver.Receive(ctx, delivery()))

	require.NotNil(t, stored)
	assert.Equal(t, models.ProviderBridge, stored.Provider)
	assert.Equal(t, "evt_1", stored.EventID)
	assert.Equal(t, "transfer.updated", stored.EventType)
	assert.Equal(t, receivedBody, stored.RawPayload)
	assert.WithinDuration(t, time.Now(), stored.ReceivedAt, time.Minute)
	require.NotNil(t, stored.ExternalRef)
	assert.Equal(t, "trf_1", *stored.ExternalRef, "an order id wins over a customer id")
}

func TestWebhookReceiver_Receive_ExternalRef(t *testing.T) {
	cases := []struct {
		name   string
		result *ramp.WebhookResult
		want   *string
	}{
		{"customer id when the event names no order", &ramp.WebhookResult{EventID: "evt_1", ExternalCustomerID: "cus_1"}, support.Ptr("cus_1")},
		{"none when the event names neither", &ramp.WebhookResult{EventID: "evt_1"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newReceiverHarness(t)
			ctx := context.Background()
			h.verifies(ctx, c.result)
			var stored *models.WebhookEvent
			h.stores(1, true, &stored)
			h.dispatcher.EXPECT().Dispatch(uint(1)).Return(nil).Once()

			require.NoError(t, h.receiver.Receive(ctx, delivery()))

			assert.Equal(t, c.want, stored.ExternalRef)
		})
	}
}

// Without a provider event id the verified body is the delivery's identity, so
// a verbatim replay collapses onto the row the first copy created.
func TestWebhookReceiver_Receive_DedupsOnTheBodyHashWithoutAnEventID(t *testing.T) {
	h := newReceiverHarness(t)
	ctx := context.Background()
	h.verifies(ctx, &ramp.WebhookResult{EventType: "virtual_account.activity"})
	var stored *models.WebhookEvent
	h.stores(3, true, &stored)
	h.dispatcher.EXPECT().Dispatch(uint(3)).Return(nil).Once()

	require.NoError(t, h.receiver.Receive(ctx, delivery()))

	assert.Equal(t, "sha256:b2bde4751ea4124ee6f5c242627ce1780578478607b398f9913c58f1079b7e0f", stored.EventID)
}

func TestWebhookReceiver_Receive_DuplicateIsAcknowledgedWithoutDispatch(t *testing.T) {
	h := newReceiverHarness(t)
	ctx := context.Background()
	h.verifies(ctx, &ramp.WebhookResult{EventID: "evt_1"})
	var stored *models.WebhookEvent
	h.stores(0, false, &stored)

	require.NoError(t, h.receiver.Receive(ctx, delivery()))
}

func TestWebhookReceiver_Receive_PersistFailureIsNotARejection(t *testing.T) {
	h := newReceiverHarness(t)
	ctx := context.Background()
	h.verifies(ctx, &ramp.WebhookResult{EventID: "evt_1"})
	cause := errors.New("connection refused")
	h.events.EXPECT().CreateIfNew(testifymock.Anything).Return(false, cause).Once()

	err := h.receiver.Receive(ctx, delivery())

	require.ErrorIs(t, err, cause)
	assert.NotErrorIs(t, err, rampsvc.ErrWebhookRejected)
}

// A lost dispatch leaves the row stored and unprocessed, which is what
// `webhooks:reprocess` picks up, so the delivery still counts as received.
func TestWebhookReceiver_Receive_DispatchFailureIsLoggedNotReturned(t *testing.T) {
	logger := &mockslog.Log{}
	mock.Factory().App().On("MakeLog").Return(logger)
	dispatchErr := errors.New("redis: connection refused")
	logger.EXPECT().WithContext(testifymock.Anything).Return(logger).Once()
	logger.EXPECT().Errorf("ramp webhook: dispatch %s event: %v", models.ProviderBridge, dispatchErr).Once()

	h := newReceiverHarness(t)
	ctx := context.Background()
	h.verifies(ctx, &ramp.WebhookResult{EventID: "evt_1"})
	var stored *models.WebhookEvent
	h.stores(7, true, &stored)
	h.dispatcher.EXPECT().Dispatch(uint(7)).Return(dispatchErr).Once()

	require.NoError(t, h.receiver.Receive(ctx, delivery()))
	logger.AssertExpectations(t)
}
