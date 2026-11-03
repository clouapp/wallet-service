package ramp_test

import (
	"context"
	"errors"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// recordingRefresher is a KycRefresher that remembers who it was asked about
// and answers err.
type recordingRefresher struct {
	asked []uint
	err   error
}

func (r *recordingRefresher) Refresh(_ context.Context, userID uint, _ string) (*models.CustomerProviderKyc, error) {
	r.asked = append(r.asked, userID)
	return nil, r.err
}

func externalAccountEvent() *ramp.WebhookResult {
	return &ramp.WebhookResult{
		Kind: ramp.WebhookKindExternalAccount, EventID: "evt_1",
		EventType: "external_account.created", ExternalCustomerID: "cus_1",
	}
}

// A bank account registered at Bridge clears `external_account` from the
// customer's requirements without a customer event, so the event re-reads the
// user's KYC row.
func TestExternalAccountEventRefreshesTheUsersKyc(t *testing.T) {
	h := newHarness(t)
	refresher := &recordingRefresher{}
	h.processor.WithKycRefresher(refresher)
	row := eventRow()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(externalAccountEvent(), nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()
	h.integRepo.EXPECT().FindByExternalCustomerID(models.ProviderBridge, "cus_1").
		Return(&models.CustomerProviderIntegration{CustomerID: 7}, nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, []uint{7}, refresher.asked)
	require.Nil(t, row.ProcessingError)
}

// A refresh that fails is logged, never the event's failure: the delivery was
// applied, and failing it would only make the provider retry it.
func TestExternalAccountEventSurvivesAFailedRefresh(t *testing.T) {
	h := newHarness(t)
	refresher := &recordingRefresher{err: errors.New("bridge down")}
	h.processor.WithKycRefresher(refresher)
	row := eventRow()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(externalAccountEvent(), nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()
	h.integRepo.EXPECT().FindByExternalCustomerID(models.ProviderBridge, "cus_1").
		Return(&models.CustomerProviderIntegration{CustomerID: 7}, nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, []uint{7}, refresher.asked)
	require.Nil(t, row.ProcessingError)
}

// Without a refresher the event is recorded and acknowledged, as before.
func TestExternalAccountEventWithoutARefresherIsOnlyRecorded(t *testing.T) {
	h := newHarness(t)
	row := eventRow()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(externalAccountEvent(), nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
}
