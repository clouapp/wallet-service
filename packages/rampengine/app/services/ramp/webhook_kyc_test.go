package ramp_test

import (
	"context"
	"testing"
	"time"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

// atProvider points the harness at a provider of its own: the events it
// processes come from provider, decoded by an adapter with that code.
func atProvider(t *testing.T, h *harness, provider string) *models.WebhookEvent {
	t.Helper()
	adapter := mocks.NewMockProviderAdapter(t)
	adapter.EXPECT().Code().Return(provider).Maybe()
	adapter.EXPECT().Supports(testifymock.Anything).Return(true).Maybe()
	h.adapter = adapter
	h.processor = rampsvc.NewWebhookProcessor(rampsvc.WebhookProcessorDeps{
		Factory: ramp.NewFactory(adapter), Events: h.events, Integrations: h.integRepo, Accounts: h.accounts,
		Members: h.members, Broadcaster: h.broadcaster, Notifier: h.notifier, Features: openFeatures(),
		Deliverer: h.deliverer,
	})
	row := eventRow()
	row.Provider = provider
	return row
}

func (h *harness) expectKycEventAbout(customer string) {
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindKyc, ExternalCustomerID: customer, Status: models.KycStatusRejected,
	}, nil).Once()
}

// A provider-side customer XIP detached to retry with a new one keeps sending
// events about itself. Each is acknowledged and writes nothing: no KYC row is
// read or written, and the event is stamped processed without an error.
func TestProcessAcknowledgesAKycEventForADetachedCustomer(t *testing.T) {
	h := newHarness(t)
	row := atProvider(t, h, models.ProviderBlindPay)
	h.storedEvent(row)
	h.expectKycEventAbout("cu_old")
	h.integRepo.EXPECT().FindByExternalCustomerID(models.ProviderBlindPay, "cu_old").Return(nil, nil).Once()
	h.integRepo.EXPECT().IsDetachedCustomer(models.ProviderBlindPay, "cu_old").Return(true, nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.True(t, row.Applied())
	h.db.AssertNotCalled(t, "FindKycForUpdate", testifymock.Anything, testifymock.Anything)
	h.db.AssertNotCalled(t, "UpdateKyc", testifymock.Anything)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
	require.Empty(t, h.verdicts)
}

// A customer no link names and none was detached from may be one whose create
// has not committed its link yet: the event fails, stamped with the error and
// left replayable — at every provider.
func TestProcessFailsAKycEventForACustomerNeverLinked(t *testing.T) {
	for _, provider := range []string{models.ProviderBridge, models.ProviderBlindPay} {
		t.Run(provider, func(t *testing.T) {
			h := newHarness(t)
			row := atProvider(t, h, provider)
			h.storedEvent(row)
			h.expectKycEventAbout("cu_new")
			h.integRepo.EXPECT().FindByExternalCustomerID(provider, "cu_new").Return(nil, nil).Once()
			h.integRepo.EXPECT().IsDetachedCustomer(provider, "cu_new").Return(false, nil).Once()
			h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

			require.Error(t, h.processor.Process(context.Background(), eventRowID))
			require.NotNil(t, row.ProcessingError)
			require.False(t, row.Applied(), "a replay applies it once the link is there")
		})
	}
}

// And the sweeper, which re-queues only events with no processed_at, takes a
// detached customer's event once and never again: it is not retried forever.
func TestSweeperTakesADetachedCustomersEventOnce(t *testing.T) {
	h := newHarness(t)
	row := atProvider(t, h, models.ProviderBlindPay)
	row.ReceivedAt = time.Now().Add(-time.Hour)
	h.storedEvent(row)
	h.expectKycEventAbout("cu_old")
	h.integRepo.EXPECT().FindByExternalCustomerID(models.ProviderBlindPay, "cu_old").Return(nil, nil).Once()
	h.integRepo.EXPECT().IsDetachedCustomer(models.ProviderBlindPay, "cu_old").Return(true, nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()
	h.events.EXPECT().FindUnprocessed(testifymock.Anything, 10).
		RunAndReturn(func(time.Time, int) ([]models.WebhookEvent, error) {
			if row.ProcessedAt != nil {
				return nil, nil
			}
			return []models.WebhookEvent{*row}, nil
		}).Twice()
	dispatcher := mocks.NewMockEventDispatcher(t)
	dispatcher.EXPECT().Dispatch(row.ID).RunAndReturn(func(id uint) error {
		return h.processor.Process(context.Background(), id)
	}).Once()
	sweeper := rampsvc.NewWebhookSweeper(h.events, dispatcher)

	first, err := sweeper.Sweep(time.Minute, 10)
	require.NoError(t, err)
	require.Equal(t, 1, first.Dispatched)
	second, err := sweeper.Sweep(time.Minute, 10)
	require.NoError(t, err)
	require.Zero(t, second.Scanned, "a processed event is not swept again")
}

// BlindPay deleted the customer: the link is switched off — the router no
// longer routes through it — and the KYC row is left as it stands.
func TestProcessCustomerDeleteSwitchesTheIntegrationOff(t *testing.T) {
	h := newHarness(t)
	row := atProvider(t, h, models.ProviderBlindPay)
	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindKyc, EventType: "customer.delete", ExternalCustomerID: "cu_9", IntegrationDisabled: true,
	}, nil).Once()
	customer := "cu_9"
	link := &models.CustomerProviderIntegration{CustomerID: 7, Provider: models.ProviderBlindPay,
		ExternalCustomerID: &customer, Status: models.IntegrationStatusActive, Metadata: "{}"}
	h.integRepo.EXPECT().FindByExternalCustomerID(models.ProviderBlindPay, "cu_9").Return(link, nil).Once()
	var written *models.CustomerProviderIntegration
	h.db.EXPECT().UpdateIntegration(testifymock.Anything).RunAndReturn(func(l *models.CustomerProviderIntegration) error {
		written = l
		return nil
	}).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.True(t, row.Applied())
	require.NotNil(t, written)
	require.Equal(t, models.IntegrationStatusDisabled, written.Status)
	require.False(t, written.IsUsable())
	h.db.AssertNotCalled(t, "UpdateKyc", testifymock.Anything)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

// tos.accept with a customer is a KYC event with no status and no payload to
// keep: the row's raw_status still holds the last customer read, and nothing
// the event did not say is written.
func TestProcessTosAcceptLeavesTheRawStatus(t *testing.T) {
	h := newHarness(t)
	h.processor = rampsvc.NewWebhookProcessor(rampsvc.WebhookProcessorDeps{
		Factory: ramp.NewFactory(blindpay.New(nil, nil, nil)), Events: h.events, Integrations: h.integRepo, Accounts: h.accounts,
		Members: h.members, Broadcaster: h.broadcaster, Notifier: h.notifier, Features: openFeatures(),
		Deliverer: h.deliverer,
	})
	row := eventRow()
	row.Provider = models.ProviderBlindPay
	row.RawPayload = `{"webhook_event":"tos.accept","tos_id":"to_1","idempotency_key":"k","customer_id":"cu_9"}`
	h.storedEvent(row)
	customer := "cu_9"
	h.integRepo.EXPECT().FindByExternalCustomerID(models.ProviderBlindPay, "cu_9").Return(&models.CustomerProviderIntegration{
		CustomerID: 7, Provider: models.ProviderBlindPay, ExternalCustomerID: &customer, Status: models.IntegrationStatusActive,
	}, nil).Once()
	const lastRead = `{"id":"cu_9","kyc_status":"approved"}`
	kycRow := &models.CustomerProviderKyc{CustomerID: 7, Provider: models.ProviderBlindPay, KycStatus: models.KycStatusApproved,
		TosStatus: models.TosStatusApproved, RawStatus: lastRead,
		Endorsements: "[]", Requirements: "[]", RejectionReasons: "[]", RejectionCategories: "[]", RejectionDetails: "[]"}
	h.db.EXPECT().FindKycForUpdate(uint(7), models.ProviderBlindPay).Return(kycRow, nil).Once()
	var written *models.CustomerProviderKyc
	h.db.EXPECT().UpdateKyc(testifymock.Anything).RunAndReturn(func(r *models.CustomerProviderKyc) error {
		written = r
		return nil
	}).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.NotNil(t, written)
	require.Equal(t, lastRead, written.RawStatus, "the acceptance is not the customer")
	require.Equal(t, models.KycStatusApproved, written.KycStatus)
}
