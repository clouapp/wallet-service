package kyc_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
)

func (h *fanOutHarness) allowTos() {
	for _, adapter := range h.adapters {
		adapter.EXPECT().RequiresTos().Return(true).Maybe()
	}
}

func (h *fanOutHarness) inFlight(provider, kind string) {
	h.staging.EXPECT().InFlightKind(uint(7), provider).Return(kind).Once()
}

// 28/09 18:04:28 UTC, user 21: the application landed and Bridge answered
// incomplete, asking for the document and the selfie — which were already
// staged behind it. The staged submission is what says so; without it the
// user was sent back to the wizard for files they had just sent.
func TestOverviewCountsTheSubmissionWindowAsInReview(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	h.allowTos()
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderKyc{
		providerState(models.ProviderBridge, models.KycStatusIncomplete, "government_id", "live_selfie"),
	}, nil).Once()
	h.integRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderIntegration{
		linkFor(models.ProviderBridge, "cus_1"),
	}, nil).Once()
	h.inFlight(models.ProviderBridge, kyc.KindDocuments)

	before := time.Now().UTC().Add(-time.Second)
	view, err := h.svc.Overview(7)
	require.NoError(t, err)
	require.Equal(t, kyc.Overall{Status: kyc.OverallInReview}, view.Overall)
	require.Len(t, view.Providers, 1)
	require.True(t, view.CheckedAt.After(before))
}

// The same row once the documents are no longer in flight is asking the user.
func TestOverviewAsksForWhatTheRowOwesWhenNothingIsInFlight(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	h.allowTos()
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderKyc{
		providerState(models.ProviderBridge, models.KycStatusIncomplete, "government_id", "live_selfie"),
	}, nil).Once()
	h.integRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderIntegration{
		linkFor(models.ProviderBridge, "cus_1"),
	}, nil).Once()
	h.inFlight(models.ProviderBridge, "")

	view, err := h.svc.Overview(7)
	require.NoError(t, err)
	require.Equal(t, kyc.OverallActionRequired, view.Overall.Status)
	require.Equal(t, []string{"document", "selfie"}, view.Overall.PendingSteps)
}

func TestOverviewOfAFreshUserIsNotStarted(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	h.allowTos()
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return(nil, nil).Once()
	h.integRepo.EXPECT().ListByCustomer(uint(7)).Return(nil, nil).Once()
	h.inFlight(models.ProviderBridge, "")

	view, err := h.svc.Overview(7)
	require.NoError(t, err)
	require.Equal(t, kyc.OverallNotStarted, view.Overall.Status)
	require.Equal(t, []string{"personal", "profile", "address", "document", "selfie"}, view.Overall.PendingSteps)
}

// A submission that failed before any provider answered put the row back to
// not_started, recording why: the user has been through the wizard.
func TestOverviewAfterAFailedSubmissionIsActionRequired(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	h.allowTos()
	failed := providerState(models.ProviderBridge, models.KycStatusNotStarted)
	failed.RecordSubmissionFailure("provider_busy", "bridge: POST /customers: upstream rejected (rate_limited)")
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderKyc{failed}, nil).Once()
	h.integRepo.EXPECT().ListByCustomer(uint(7)).Return(nil, nil).Once()
	h.inFlight(models.ProviderBridge, "")

	view, err := h.svc.Overview(7)
	require.NoError(t, err)
	require.Equal(t, kyc.OverallActionRequired, view.Overall.Status)
}

// Whether the user submitted before is a fact about the user: a provider no
// longer enabled still says so, and the one enabled since is owed the
// application by someone who has already been through the wizard.
func TestOverviewReadsSubmittedBeforeFromEveryStoredRow(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	h.allowTos()
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderKyc{
		providerState(models.ProviderOzav, models.KycStatusRejected),
	}, nil).Once()
	h.integRepo.EXPECT().ListByCustomer(uint(7)).Return(nil, nil).Once()
	h.inFlight(models.ProviderBridge, "")

	view, err := h.svc.Overview(7)
	require.NoError(t, err)
	require.Len(t, view.Providers, 1, "the disabled provider is not listed")
	require.Equal(t, kyc.OverallActionRequired, view.Overall.Status)
}

// The listing survives a failed read of the integrations by leaving rows
// unmarked; the overview cannot: an unmarked fresh user owes nothing it can
// see, and would read as under review. It answers the failure instead.
func TestOverviewFailsWhenTheIntegrationsCannotBeRead(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	h.allowTos()
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return(nil, nil).Once()
	h.integRepo.EXPECT().ListByCustomer(uint(7)).Return(nil, errors.New("pq: connection reset")).Once()

	_, err := h.svc.Overview(7)
	require.Error(t, err)
}
