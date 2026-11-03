package kyc_test

import (
	"context"
	"sync"
	"testing"

	"github.com/goravel/framework/support/carbon"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
)

// storedKyc is a one-row store with the repository's write semantics: an
// unconditional Update/UpdateRecorded, and UpdateIfStatus/SaveWithLink that
// land only on the status they name.
type storedKyc struct {
	mu   sync.Mutex
	row  models.CustomerProviderKyc
	link *models.CustomerProviderIntegration
}

func (s *storedKyc) read() *models.CustomerProviderKyc {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.row
	return &row
}

func (s *storedKyc) writeIf(row *models.CustomerProviderKyc, status string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.row.KycStatus != status {
		return false
	}
	s.row = *row
	return true
}

func backStore(h *harness, store *storedKyc) {
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		RunAndReturn(func(uint, string) (*models.CustomerProviderKyc, error) { return store.read(), nil })
	h.kycRepo.EXPECT().UpdateRecorded(testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(row *models.CustomerProviderKyc, _ *models.AdminAuditLog) error {
			store.mu.Lock()
			defer store.mu.Unlock()
			store.row = *row
			return nil
		}).Maybe()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(row *models.CustomerProviderKyc, status string) (bool, error) {
			return store.writeIf(row, status), nil
		}).Maybe()
	h.kycRepo.EXPECT().SaveWithLink(testifymock.Anything, false, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(row *models.CustomerProviderKyc, _ bool, link *models.CustomerProviderIntegration, status string) (bool, error) {
			store.mu.Lock()
			linked := *link
			store.link = &linked
			store.mu.Unlock()
			return store.writeIf(row, status), nil
		}).Maybe()
}

// The worker's success write against an admin reset that lands during the
// send. A reset refuses a row a submission is in flight on, except one past
// the staging TTL (nothing can complete it any more) — which is exactly a
// documents job re-queued up to that edge and still sending. The worker read
// the row before the send; its answer used to be written over the reset,
// undoing the support action the operator had just taken.
func TestAWorkerAnswerDoesNotUndoAnAdminResetThatLandedDuringTheSend(t *testing.T) {
	h := newHarness(t)
	store := &storedKyc{row: *kycRow(models.KycStatusSubmitting)}
	store.row.UpdatedAt = carbon.NewDateTime(carbon.Now().SubMinutes(16))
	store.row.SetRejectionCategories([]string{models.KycRejectionSelfieMismatch})
	backStore(h, store)
	app := stagedApplication()
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindApplication,
		Application: &app, PreviousStatus: models.KycStatusIncomplete,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(linkedIntegration(""), nil)
	h.staging.EXPECT().Forget("stg_1").Once()
	// The reset announces itself; the worker's answer must not follow it.
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.KycStatus == models.KycStatusNotStarted
	})).Once()
	h.adapter.EXPECT().SubmitApplication(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(context.Context, ramp.CustomerRef, ramp.Application) (*ramp.KycState, error) {
			_, err := h.svc.ResetVerdict(context.Background(), 7, models.ProviderBridge, recordedAs("kyc.reset"))
			require.NoError(t, err, "the reset lands while the provider is answering")
			return &ramp.KycState{Status: models.KycStatusUnderReview, ExternalCustomerID: "cus_new"}, nil
		}).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))

	stored := store.read()
	require.Equal(t, models.KycStatusNotStarted, stored.KycStatus, "the reset stands")
	require.Empty(t, stored.DecodedRejectionCategories())
	require.NotNil(t, store.link, "the customer the send created is still linked")
	require.Equal(t, "cus_new", *store.link.ExternalCustomerID)
}

// A refresh reads the row, asks the provider, and writes the answer. When the
// row moved during the call its answer is not written over it, nothing is
// announced, and the caller gets what the row holds.
func TestARefreshDoesNotWriteOverARowThatMovedDuringTheCall(t *testing.T) {
	h := newHarness(t)
	store := &storedKyc{row: *kycRow(models.KycStatusUnderReview)}
	backStore(h, store)
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	h.adapter.EXPECT().RefreshKyc(testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(context.Context, ramp.CustomerRef) (*ramp.KycState, error) {
			// A webhook lands with the provider's final answer.
			moved := store.read()
			moved.KycStatus = models.KycStatusRejected
			moved.SetRejectionCategories([]string{models.KycRejectionNotEligible})
			store.writeIf(moved, models.KycStatusUnderReview)
			return &ramp.KycState{Status: models.KycStatusUnderReview}, nil
		}).Once()

	got, err := h.svc.Refresh(context.Background(), 7, models.ProviderBridge)

	require.NoError(t, err)
	require.Equal(t, models.KycStatusRejected, got.KycStatus)
	require.Equal(t, models.KycStatusRejected, store.read().KycStatus)
	h.broadcaster.AssertNotCalled(t, "PublishKyc", testifymock.Anything, testifymock.Anything)
}

// A hosted link decides nothing about the status, so when the row moved
// during the provider call the link is written onto what the row holds now.
func TestAHostedLinkIsWrittenOntoTheRowAsItIsNow(t *testing.T) {
	h := newHarness(t)
	store := &storedKyc{row: *kycRow(models.KycStatusIncomplete)}
	backStore(h, store)
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	h.adapter.EXPECT().CreateHostedKycLink(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(context.Context, ramp.CustomerRef, ramp.HostedLinkOpts) (*ramp.HostedKycLink, error) {
			moved := store.read()
			moved.KycStatus = models.KycStatusUnderReview
			store.writeIf(moved, models.KycStatusIncomplete)
			return &ramp.HostedKycLink{LinkID: "kyc_link_1", URL: "https://bridge.xyz/kyc/abc", ExternalCustomerID: "cus_1"}, nil
		}).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	got, err := h.svc.HostedLink(context.Background(), h.user, 7, models.ProviderBridge, dto.HostedLinkDTO{FullName: "Luiza Souza"})

	require.NoError(t, err)
	stored := store.read()
	require.Equal(t, models.KycStatusUnderReview, stored.KycStatus, "the status that landed is kept")
	require.NotNil(t, stored.HostedKycURL)
	require.Equal(t, "https://bridge.xyz/kyc/abc", *stored.HostedKycURL)
	require.Equal(t, models.KycStatusUnderReview, got.KycStatus)
}
