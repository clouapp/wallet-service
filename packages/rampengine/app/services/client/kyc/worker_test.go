package kyc_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
)

// The payload is somebody's identity documents: a job that cannot send it drops
// it at once rather than leaving it to the TTL.
func TestProcessStagedSubmissionDropsThePayloadOfAnUnknownProvider(t *testing.T) {
	h := newHarness(t)
	h.staging.EXPECT().Forget("stg_1").Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, "nope"))
}

// Nothing else ever clears `submitting`: a submission that cannot be sent drops
// its payload and releases the row with the reason, and the app hears it.
func TestProcessStagedSubmissionReleasesTheRowOfASubmissionItCannotSend(t *testing.T) {
	app, docs := stagedApplication(), stagedDocuments()
	cases := map[string]struct {
		submission *kyc.StagedSubmission
		link       *models.CustomerProviderIntegration
	}{
		"link vanished": {
			submission: &kyc.StagedSubmission{Kind: kyc.KindApplication, Application: &app,
				PreviousStatus: models.KycStatusNotStarted},
		},
		"documents with no customer, no worker to wait on": {
			submission: &kyc.StagedSubmission{Kind: kyc.KindDocuments, Documents: &docs,
				PreviousStatus: models.KycStatusSubmitting},
			link: linkedIntegration(""),
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			c.submission.UserID, c.submission.Provider = 7, models.ProviderBridge
			h.staging.EXPECT().Get("stg_1").Return(c.submission, nil).Once()
			h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(c.link, nil).Once()
			h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
				Return(kycRow(models.KycStatusSubmitting), nil).Once()
			h.staging.EXPECT().Forget("stg_1").Once()
			var released *models.CustomerProviderKyc
			h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once().
				Run(func(args testifymock.Arguments) { released = args.Get(0).(*models.CustomerProviderKyc) })
			h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
				return e.FailureReason == "submission_failed"
			})).Once()

			require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
			require.Equal(t, models.KycStatusIncomplete, released.KycStatus)
			require.NotNil(t, released.LastErrorCode)
			h.adapter.AssertNotCalled(t, "SubmitApplication", testifymock.Anything, testifymock.Anything, testifymock.Anything)
		})
	}
}

// No later event would ever clear `submitting` for a provider that accepted
// without saying what it is doing.
func TestProcessStagedSubmissionReadsAnAcceptanceWithoutStatusAsUnderReview(t *testing.T) {
	h := newHarness(t)
	app := stagedApplication()
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindApplication,
		Application: &app, PreviousStatus: models.KycStatusNotStarted,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusSubmitting), nil).Once()
	h.adapter.EXPECT().SubmitApplication(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{ExternalCustomerID: "cus_1"}, nil).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.KycStatus == models.KycStatusUnderReview
	})).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
}

// busyDocuments stages documents for a customer that exists, on a row the
// application ahead already moved on (the shape of user 14 on 01/10/2026), and
// has the provider refuse them with err.
func busyDocuments(t *testing.T, h *harness, stagedAt time.Time, err error) {
	t.Helper()
	docs := stagedDocuments()
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindDocuments,
		Documents: &docs, PreviousStatus: models.KycStatusSubmitting, StagedAt: stagedAt,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusIncomplete), nil).Once()
	h.adapter.EXPECT().SubmitDocuments(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, err).Once()
}

// Measured in production on 01/10/2026: Bridge refused documents sent moments
// after the customer was created, and the worker dropped them. The app had its
// 202, the provider kept asking for the documents, and nothing on this side
// was ever going to send them. A failure the provider calls temporary — or a
// request that provably never left — is the same submission later, so the
// payload stays staged and the job goes back on the queue. Until then the
// documents are still in flight: nothing is published and the row is untouched.
func TestProcessStagedDocumentsRequeuesATransientFailure(t *testing.T) {
	cases := map[string]error{
		"provider busy":      fmt.Errorf("bridge: PUT /customers/cus_1: %w", ramp.ErrRetryableUpstream),
		"request never left": fmt.Errorf("%w: %w: dial tcp: timeout", ramp.ErrProviderFailed, ramp.ErrRequestNotSent),
	}
	for name, err := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarnessWithQueue(t, "database")
			busyDocuments(t, h, time.Now().Add(-20*time.Second), err)
			h.dispatcher.EXPECT().
				DispatchAfter("stg_1", uint(7), models.ProviderBridge, testifymock.Anything).
				Return(nil).Once()

			require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
			h.staging.AssertNotCalled(t, "Forget", "stg_1")
		})
	}
}

// A refusal of what was sent is the same refusal on every attempt: the
// documents end there, as any failure did before, and the app is told.
func TestProcessStagedDocumentsDoesNotRequeueARefusalOfTheBody(t *testing.T) {
	h := newHarnessWithQueue(t, "database")
	busyDocuments(t, h, time.Now(), fmt.Errorf("bridge: PUT /customers/cus_1: invalid_parameters: %w", ramp.ErrProviderFailed))
	h.staging.EXPECT().Forget("stg_1").Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusIncomplete).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
	h.dispatcher.AssertNotCalled(t, "DispatchAfter",
		testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// The wait grows with the submission's age, so a provider still busy after a
// few seconds is not hammered with the same upload, between a floor that gives
// a customer being processed time to settle and a ceiling that still fits
// several attempts inside the staging TTL.
func TestProcessStagedDocumentsBacksOffAsTheSubmissionAges(t *testing.T) {
	cases := map[string]struct {
		age      time.Duration
		min, max time.Duration
	}{
		"just staged":     {age: time.Second, min: 5 * time.Second, max: 5 * time.Second},
		"twenty seconds":  {age: 20 * time.Second, min: 19 * time.Second, max: 22 * time.Second},
		"several minutes": {age: 5 * time.Minute, min: time.Minute, max: time.Minute},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarnessWithQueue(t, "database")
			busyDocuments(t, h, time.Now().Add(-c.age), ramp.ErrRetryableUpstream)
			var delay time.Duration
			h.dispatcher.EXPECT().
				DispatchAfter("stg_1", uint(7), models.ProviderBridge, testifymock.Anything).
				Run(func(_ string, _ uint, _ string, d time.Duration) { delay = d }).
				Return(nil).Once()

			require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
			require.GreaterOrEqual(t, delay, c.min)
			require.LessOrEqual(t, delay, c.max)
		})
	}
}

// A retry that would land after the staging TTL would find nothing to send,
// and the expiry path has no word for the app. So the worker gives up while it
// still holds the payload, the way it gives up on any failure: it drops the
// documents and tells the app. A payload staged before StagedAt existed has
// no age to go by and is treated as out of time.
func TestProcessStagedDocumentsGivesUpOnATransientFailureOutOfTime(t *testing.T) {
	cases := map[string]time.Time{
		"near the staging deadline": time.Now().Add(-(kyc.StagingTTL - 30*time.Second)),
		"staged by an older build":  {},
	}
	for name, stagedAt := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarnessWithQueue(t, "database")
			busyDocuments(t, h, stagedAt, ramp.ErrRetryableUpstream)
			h.staging.EXPECT().Forget("stg_1").Once()
			h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusIncomplete).Return(true, nil).Once()
			h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
				return e.FailureReason == "submission_failed" && e.FailureCode == "provider_busy"
			})).Once()

			require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
			h.dispatcher.AssertNotCalled(t, "DispatchAfter",
				testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
		})
	}
}

// Under the sync driver there is no later to re-queue into, and when the queue
// refuses the re-queue there is no later either: both end the submission.
func TestProcessStagedDocumentsGivesUpWhenItCannotRequeueATransientFailure(t *testing.T) {
	t.Run("sync driver", func(t *testing.T) {
		h := newHarness(t)
		busyDocuments(t, h, time.Now(), ramp.ErrRetryableUpstream)
		h.staging.EXPECT().Forget("stg_1").Once()
		h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusIncomplete).Return(true, nil).Once()
		h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

		require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
		h.dispatcher.AssertNotCalled(t, "DispatchAfter",
			testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
	})
	t.Run("queue refuses", func(t *testing.T) {
		h := newHarnessWithQueue(t, "database")
		busyDocuments(t, h, time.Now(), ramp.ErrRetryableUpstream)
		h.dispatcher.EXPECT().
			DispatchAfter("stg_1", uint(7), models.ProviderBridge, testifymock.Anything).
			Return(errors.New("queue down")).Once()
		h.staging.EXPECT().Forget("stg_1").Once()
		h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusIncomplete).Return(true, nil).Once()
		h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

		require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
	})
}

// An application is not retried here: its failure reaches the app while the
// form is still in hand, and only documents were being lost after a 202.
func TestProcessStagedApplicationDoesNotRequeueATransientFailure(t *testing.T) {
	h := newHarnessWithQueue(t, "database")
	app := stagedApplication()
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindApplication,
		Application: &app, PreviousStatus: models.KycStatusIncomplete, StagedAt: time.Now(),
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusSubmitting), nil).Once()
	h.adapter.EXPECT().SubmitApplication(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, ramp.ErrRetryableUpstream).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
	h.dispatcher.AssertNotCalled(t, "DispatchAfter",
		testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// Documents queued behind the application that created the customer meet a
// row the application already settled (incomplete). A failure there used to
// be dropped on the floor: the row kept no last_error and the app, told only
// "submission_failed", had nothing to say (PF documents investigation, round
// 4). The failure is recorded on the settled row, written only while it
// still holds the status it was read with, and announced with its code.
func TestProcessStagedDocumentsRecordsAFailureOnTheRowTheApplicationSettled(t *testing.T) {
	cases := map[string]struct {
		err        error
		code       string
		categories []string
	}{
		"a document the provider refused": {
			fmt.Errorf("bridge: PUT /customers/cus_1: invalid_parameters: liveness_check_selfies: %w", ramp.ErrDocumentRefused),
			models.KycRejectionDocumentUnreadable, []string{models.KycRejectionDocumentUnreadable},
		},
		"a provider failure": {
			fmt.Errorf("bridge: PUT /customers/cus_1: internal_error: %w", ramp.ErrProviderFailed),
			ramp.DefaultErrorCode, nil,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarnessWithQueue(t, "database")
			busyDocuments(t, h, time.Now(), c.err)
			h.staging.EXPECT().Forget("stg_1").Once()
			var recorded *models.CustomerProviderKyc
			h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusIncomplete).Return(true, nil).Once().
				Run(func(args testifymock.Arguments) { recorded = args.Get(0).(*models.CustomerProviderKyc) })
			h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
				return e.FailureReason == "submission_failed" && e.FailureCode == c.code
			})).Once()

			require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
			require.Equal(t, models.KycStatusIncomplete, recorded.KycStatus, "the provider's status stands")
			require.NotNil(t, recorded.LastErrorCode)
			require.Equal(t, c.code, *recorded.LastErrorCode)
			require.Equal(t, c.categories, recorded.DecodedRejectionCategories())
		})
	}
}

// A row that moved on while the documents were refused is not written over,
// and the refusal is not announced over it.
func TestProcessStagedDocumentsDoesNotRecordAFailureOverARowThatMoved(t *testing.T) {
	h := newHarnessWithQueue(t, "database")
	busyDocuments(t, h, time.Now(), fmt.Errorf("bridge: %w", ramp.ErrDocumentRefused))
	h.staging.EXPECT().Forget("stg_1").Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusIncomplete).Return(false, nil).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
	h.broadcaster.AssertNotCalled(t, "PublishKyc", testifymock.Anything, testifymock.Anything)
}

// Documents refused while the row is still theirs release it as any failure
// does, with the category that reopens the document step; nobody is told the
// provider rejected them, because it did not.
func TestProcessStagedDocumentsReleasesARefusedDocumentWithItsCategory(t *testing.T) {
	h := newHarnessWithQueue(t, "database")
	docs := stagedDocuments()
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindDocuments,
		Documents: &docs, PreviousStatus: models.KycStatusIncomplete, StagedAt: time.Now(),
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusSubmitting), nil).Once()
	h.adapter.EXPECT().SubmitDocuments(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, fmt.Errorf("bridge: %w", ramp.ErrDocumentRefused)).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	var released *models.CustomerProviderKyc
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusSubmitting).Return(true, nil).Once().
		Run(func(args testifymock.Arguments) { released = args.Get(0).(*models.CustomerProviderKyc) })
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.FailureCode == models.KycRejectionDocumentUnreadable
	})).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
	require.Equal(t, models.KycStatusIncomplete, released.KycStatus)
	require.Equal(t, []string{models.KycRejectionDocumentUnreadable}, released.DecodedRejectionCategories())
}
