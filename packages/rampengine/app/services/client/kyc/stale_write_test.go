package kyc_test

import (
	"context"
	"errors"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
)

// The writes that mark a row in flight, and take it back off, are decided from
// a status read moments earlier — and the worker that resolves a submission
// writes outside the request's lock. On 30/09 (user 17, Ozav) a documents
// request read `submitting`, the application worker then wrote the provider's
// final refusal, and the request marked the row in flight from its copy a
// millisecond later: the refusal was gone, the documents waited fifteen
// minutes for a customer nothing would create, and the row ended `incomplete`.
//
// These pin each such write to the status it was decided from
// (UpdateIfStatus); the repository test proves the write itself.

// The request re-reads when its mark misses, and answers what it finds: here
// the refusal, which the documents must not be sent past.
func TestSubmitDocumentsKeepsARefusalThatLandedAfterItsRead(t *testing.T) {
	h := newHarness(t)
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(inFlightRow(models.ProviderBridge), nil).Once()
	h.staging.EXPECT().InFlightKind(uint(7), models.ProviderBridge).Return(kyc.KindApplication).Once()
	h.staging.EXPECT().Put(testifymock.Anything).Return("stg_1", nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusSubmitting).Return(false, nil).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	refused := kycRow(models.KycStatusRejected)
	refused.SetRejectionCategories([]string{models.KycRejectionNotEligible})
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(refused, nil).Once()

	_, err := h.svc.SubmitDocuments(context.Background(), h.user, 7, models.ProviderBridge, fullDocuments())

	require.ErrorIs(t, err, ramp.ErrKycRejected)
	h.kycRepo.AssertNotCalled(t, "Update", testifymock.Anything)
	h.dispatcher.AssertNotCalled(t, "Dispatch", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// A row that will not hold still is not marked at all: the client is told
// something is in flight, which it already retries.
func TestSubmitDocumentsGivesUpOnARowThatKeepsMoving(t *testing.T) {
	h := newHarness(t)
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		RunAndReturn(func(uint, string) (*models.CustomerProviderKyc, error) {
			return kycRow(models.KycStatusIncomplete), nil
		})
	h.staging.EXPECT().Put(testifymock.Anything).Return("stg_1", nil)
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusIncomplete).Return(false, nil)
	h.staging.EXPECT().Forget("stg_1")

	_, err := h.svc.SubmitDocuments(context.Background(), h.user, 7, models.ProviderBridge, fullDocuments())

	require.ErrorIs(t, err, kyc.ErrSubmissionInFlight)
	h.dispatcher.AssertNotCalled(t, "Dispatch", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// The worker's release is decided from the row it read before the send, up to
// the worker timeout earlier. If the row left `submitting` meanwhile, the
// release writes nothing and announces nothing: the frame would carry a status
// the row does not hold.
func TestProcessStagedSubmissionLeavesARowThatMovedDuringTheSend(t *testing.T) {
	h := newHarness(t)
	app := stagedApplication()
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindApplication,
		Application: &app, PreviousStatus: models.KycStatusIncomplete,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusSubmitting), nil).Once()
	h.adapter.EXPECT().SubmitApplication(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, errors.New("bridge: POST /customers: upstream rejected (invalid_parameters)")).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusSubmitting).Return(false, nil).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))

	h.broadcaster.AssertNotCalled(t, "PublishKyc", testifymock.Anything, testifymock.Anything)
	h.kycRepo.AssertNotCalled(t, "Update", testifymock.Anything)
}

// Documents dropped for want of a customer put the row back where they found
// it. They marked it in flight from `rejected` — a refusal the user can
// correct — and the release used to write `incomplete` whatever they found,
// which erased the refusal the app was showing.
func TestProcessStagedDocumentsDroppedPutTheRowBackWhereTheyFoundIt(t *testing.T) {
	h := newHarnessWithQueue(t, "database")
	docs := stagedDocuments()
	row := kycRow(models.KycStatusSubmitting)
	row.SetRejectionCategories([]string{models.KycRejectionSelfieMismatch})
	h.staging.EXPECT().Get("stg_docs").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindDocuments,
		Documents: &docs, PreviousStatus: models.KycStatusRejected,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil)
	h.staging.EXPECT().Forget("stg_docs").Once()
	h.kycRepo.EXPECT().UpdateIfStatus(row, models.KycStatusSubmitting).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.KycStatus == models.KycStatusRejected
	})).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_docs", 7, models.ProviderBridge))

	require.Equal(t, models.KycStatusRejected, row.KycStatus)
}

// The terms lock is not the submit lock, so a terms write can find its row
// moved too: it reads the row again and writes the session onto what is there
// now, never the status it first read.
func TestStartTosWritesTheSessionOntoTheRowAsItIsNow(t *testing.T) {
	h := newHarness(t)
	h.adapter.EXPECT().CreateTosSession(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.TosSession{URL: "https://ozav.io/terms", SessionID: "ts_1"}, nil).Once()
	h.adapter.EXPECT().RequiresTos().Return(true).Once()
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusNotStarted), nil).Twice()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusNotStarted).Return(false, nil).Once()
	refused := kycRow(models.KycStatusRejected)
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(refused, nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(refused, models.KycStatusRejected).Return(true, nil).Once()

	saved, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")

	require.NoError(t, err)
	require.Equal(t, models.KycStatusRejected, saved.KycStatus)
	require.NotNil(t, saved.TosSessionID)
	require.Equal(t, "ts_1", *saved.TosSessionID)
}
