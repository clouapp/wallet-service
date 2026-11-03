package kyc_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

func claimedIntent(id uint, userID uint, submissionID string, withMaterial bool) repositories.ClaimedKycIntent {
	intent := models.ProviderKycSubmissionIntent{
		ExternalID: "ext-" + submissionID, CustomerID: userID, Provider: models.ProviderBridge,
		SubmissionID: submissionID, State: models.KycIntentUnknown, Revision: 4, RecoveryAttempts: 1,
	}
	intent.ID = id
	claimed := repositories.ClaimedKycIntent{Intent: intent}
	if withMaterial {
		claimed.Material = &repositories.KycIntentMaterial{Version: 1, SubmissionID: submissionID, Prepared: atomicPrepared()}
	} else {
		claimed.Intent.State = models.KycIntentManualReview
	}
	return claimed
}

func newRecoveryHarness(t *testing.T) (*harness, *mocks.MockProviderKycSubmissionIntentRepository) {
	t.Helper()
	intents := mocks.NewMockProviderKycSubmissionIntentRepository(t)
	h, _ := newAtomicHarnessWith(t, "redis", intents)
	return h, intents
}

// The sweep claims what is due with the worker's lease, eight attempts and
// the configured retention, and hands each claimed intent with material back
// to the worker under its own submission id. One that came back quarantined
// is only counted.
func TestSweepDispatchesEveryClaimedIntent(t *testing.T) {
	h, intents := newRecoveryHarness(t)
	before := time.Now()
	intents.EXPECT().ClaimRecoveryDue(testifymock.Anything, testifymock.Anything, 100, 8, testifymock.Anything).
		RunAndReturn(func(now, leaseUntil time.Time, _, _ int, retainUntil time.Time) ([]repositories.ClaimedKycIntent, error) {
			require.WithinRange(t, now, before, time.Now())
			require.Equal(t, 150*time.Second, leaseUntil.Sub(now))
			require.Equal(t, 720*time.Hour, retainUntil.Sub(now))
			return []repositories.ClaimedKycIntent{
				claimedIntent(1, 7, "stg_a", true),
				claimedIntent(2, 8, "stg_b", false),
				claimedIntent(3, 9, "stg_c", true),
			}, nil
		}).Once()
	h.dispatcher.EXPECT().Dispatch("stg_a", uint(7), models.ProviderBridge).Return(nil).Once()
	h.dispatcher.EXPECT().Dispatch("stg_c", uint(9), models.ProviderBridge).Return(nil).Once()

	report, err := h.svc.SweepIntentRecovery(context.Background(), 100)

	require.NoError(t, err)
	require.Equal(t, kyc.IntentRecoveryReport{Claimed: 3, Dispatched: 2, Quarantined: 1}, report)
}

// A claim the queue would not take is rescheduled a minute out, so the next
// sweep finds it rather than its lease running out first. Nothing else is
// written on it.
func TestSweepReschedulesAFailedDispatch(t *testing.T) {
	h, intents := newRecoveryHarness(t)
	intents.EXPECT().ClaimRecoveryDue(testifymock.Anything, testifymock.Anything, 10, 8, testifymock.Anything).
		Return([]repositories.ClaimedKycIntent{claimedIntent(1, 7, "stg_a", true), claimedIntent(2, 8, "stg_b", true)}, nil).Once()
	h.dispatcher.EXPECT().Dispatch("stg_a", uint(7), models.ProviderBridge).Return(errors.New("redis down")).Once()
	h.dispatcher.EXPECT().Dispatch("stg_b", uint(8), models.ProviderBridge).Return(nil).Once()
	before := time.Now()
	intents.EXPECT().ScheduleRecovery(testifymock.MatchedBy(func(i *models.ProviderKycSubmissionIntent) bool {
		return i.ID == 1 && i.Revision == 4
	}), testifymock.Anything, "recovery_dispatch_failed").
		RunAndReturn(func(_ *models.ProviderKycSubmissionIntent, at time.Time, _ string) error {
			require.WithinRange(t, at, before.Add(time.Minute), time.Now().Add(time.Minute))
			return nil
		}).Once()

	report, err := h.svc.SweepIntentRecovery(context.Background(), 10)

	require.NoError(t, err)
	require.Equal(t, kyc.IntentRecoveryReport{Claimed: 2, Dispatched: 1, Failed: 1}, report)
}

// An intent the claim failed because not one create ever left is not
// dispatched: its staging is dropped and its row, still `submitting` if the
// worker died with it, is let go with a failure the user can retry from.
func TestSweepLetsGoOfAnIntentThatNeverSent(t *testing.T) {
	h, intents := newRecoveryHarness(t)
	abandoned := claimedIntent(1, 7, "stg_a", false)
	abandoned.Intent.State, abandoned.Abandoned = models.KycIntentFailed, true
	intents.EXPECT().ClaimRecoveryDue(testifymock.Anything, testifymock.Anything, 10, 8, testifymock.Anything).
		Return([]repositories.ClaimedKycIntent{abandoned}, nil).Once()
	h.staging.EXPECT().Forget("stg_a").Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(deferredRow(), nil).Once()
	var saved *models.CustomerProviderKyc
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusSubmitting).
		RunAndReturn(func(row *models.CustomerProviderKyc, _ string) (bool, error) { saved = row; return true, nil }).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.FailureReason != ""
	})).Once()

	report, err := h.svc.SweepIntentRecovery(context.Background(), 10)

	require.NoError(t, err)
	require.Equal(t, kyc.IntentRecoveryReport{Claimed: 1, Abandoned: 1}, report)
	h.dispatcher.AssertNotCalled(t, "Dispatch", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	require.NotNil(t, saved)
	require.Equal(t, models.KycStatusIncomplete, saved.KycStatus)
	require.Equal(t, ramp.DefaultErrorCode, *saved.LastErrorCode, "not submission_held")
	require.NotNil(t, saved.ApplicationDeferredAt, "the application stays given: only the documents are owed")
}

func TestSweepReturnsAFailedClaim(t *testing.T) {
	h, intents := newRecoveryHarness(t)
	intents.EXPECT().ClaimRecoveryDue(testifymock.Anything, testifymock.Anything, 100, 8, testifymock.Anything).
		Return(nil, errors.New("connection refused")).Once()

	_, err := h.svc.SweepIntentRecovery(context.Background(), 100)

	require.EqualError(t, err, "kyc: claim due intents: connection refused")
}

// The worker infers whose lease it is from last_attempt_at and the recovery
// count. The sweep claims and dispatches without writing the intent in
// between, so the dispatched run takes the lease as its own and sends.
func TestSweptIntentIsTakenByTheDispatchedRun(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	intent := w.store.seed(models.KycIntentUnknown, repositories.KycIntentMaterial{
		Version: 1, SubmissionID: "stg_1", ClientIPAddress: atomicClientIP, Prepared: atomicPrepared(),
	})
	attempted := time.Now().Add(-10 * time.Minute)
	expired := attempted.Add(150 * time.Second)
	intent.AttemptCount, intent.LastAttemptAt, intent.RecoveryLeaseExpiresAt = 1, &attempted, &expired
	w.h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).
		RunAndReturn(func(id string, userID uint, provider string) error {
			return w.h.svc.ProcessStagedSubmission(id, userID, provider)
		}).Once()
	w.staged(0)
	w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, intent.ProviderIdempotencyKey, *atomicPrepared()).
		Return(&ramp.KycState{ExternalCustomerID: "cus_bp"}, nil).Once()
	w.expectLinked()
	w.h.staging.EXPECT().Forget("stg_1").Once()
	w.h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	report, err := w.h.svc.SweepIntentRecovery(context.Background(), 100)

	require.NoError(t, err)
	require.Equal(t, kyc.IntentRecoveryReport{Claimed: 1, Dispatched: 1}, report)
	require.Equal(t, []string{
		"sweeper.Claim", "intent.FindActive", "intent.BeginSubmitting", "intent.RecordCustomer", "intent.Complete",
	}, w.journal)
	require.Equal(t, models.KycIntentCompleted, w.store.state())
	require.Equal(t, 1, w.store.intent.RecoveryAttempts)
	require.Equal(t, 2, w.store.intent.AttemptCount)
}

// --- resolve ----------------------------------------------------------------

func manualReviewIntent() *models.ProviderKycSubmissionIntent {
	intent := &models.ProviderKycSubmissionIntent{
		ExternalID: "2b1f6a0e-0000-4000-8000-000000000001", CustomerID: 7, Provider: models.ProviderBridge,
		SubmissionID: "stg_1", State: models.KycIntentManualReview, Revision: 5,
	}
	intent.ID = 1
	return intent
}

// What an operator types is checked before anything is read: an outcome
// outside the three, a reason past 255 characters, a customer on anything but
// a reconciliation, or no id at all.
func TestResolveIntentRefusesAnInvalidResolution(t *testing.T) {
	for name, resolution := range map[string]kyc.IntentResolution{
		"unknown outcome":                {ExternalID: "x", Outcome: "approved", Reason: "r"},
		"reason past 255 characters":     {ExternalID: "x", Outcome: models.KycIntentOutcomeRejected, Reason: strings.Repeat("é", 256)},
		"customer on a rejection":        {ExternalID: "x", Outcome: models.KycIntentOutcomeRejected, Reason: "r", CustomerID: "cus_1"},
		"no id":                          {Outcome: models.KycIntentOutcomeCancelled, Reason: "r"},
		"no reason":                      {ExternalID: "x", Outcome: models.KycIntentOutcomeCancelled},
		"reason of blanks":               {ExternalID: "x", Outcome: models.KycIntentOutcomeCancelled, Reason: "   "},
		"customer on a cancellation too": {ExternalID: "x", Outcome: models.KycIntentOutcomeCancelled, Reason: "r", CustomerID: "c"},
	} {
		t.Run(name, func(t *testing.T) {
			h, _ := newRecoveryHarness(t)
			err := h.svc.ResolveIntent(context.Background(), resolution)
			require.ErrorIs(t, err, repositories.ErrInvalidKycResolution)
		})
	}
}

// 255 characters is a reason, however many bytes they take.
func TestResolveIntentCountsTheReasonInCharacters(t *testing.T) {
	h, intents := newRecoveryHarness(t)
	reason := strings.Repeat("é", 255)
	intent := manualReviewIntent()
	intents.EXPECT().FindByExternalID(intent.ExternalID).Return(intent, nil, nil).Once()
	intents.EXPECT().Resolve(intent.ExternalID, models.KycIntentOutcomeRejected, reason, testifymock.Anything).Return(nil).Once()

	require.NoError(t, h.svc.ResolveIntent(context.Background(), kyc.IntentResolution{
		ExternalID: intent.ExternalID, Outcome: models.KycIntentOutcomeRejected, Reason: reason,
	}))
}

// Only an intent an operator holds is theirs to decide; anything else is
// still the automation's.
func TestResolveIntentOutsideManualReviewIsRefused(t *testing.T) {
	h, intents := newRecoveryHarness(t)
	intent := manualReviewIntent()
	intent.State = models.KycIntentUnknown
	intents.EXPECT().FindByExternalID(intent.ExternalID).Return(intent, nil, nil).Once()

	err := h.svc.ResolveIntent(context.Background(), kyc.IntentResolution{
		ExternalID: intent.ExternalID, Outcome: models.KycIntentOutcomeReconciled, Reason: "found", CustomerID: "cus_bp",
	})

	require.ErrorIs(t, err, repositories.ErrKycIntentNotResolvable)
}

func TestResolveIntentNotFound(t *testing.T) {
	h, intents := newRecoveryHarness(t)
	intents.EXPECT().FindByExternalID("nope").Return(nil, nil, nil).Once()

	err := h.svc.ResolveIntent(context.Background(), kyc.IntentResolution{
		ExternalID: "nope", Outcome: models.KycIntentOutcomeCancelled, Reason: "dup",
	})

	require.ErrorIs(t, err, repositories.ErrKycIntentNotFound)
}

// An intent whose material no longer opens is still one to decide: its
// state is what Resolve checks, not its material.
func TestResolveIntentWithUnreadableMaterial(t *testing.T) {
	h, intents := newRecoveryHarness(t)
	intent := manualReviewIntent()
	intents.EXPECT().FindByExternalID(intent.ExternalID).Return(intent, nil, repositories.ErrKycIntentIntegrity).Once()
	intents.EXPECT().Resolve(intent.ExternalID, models.KycIntentOutcomeCancelled, "tampered", testifymock.Anything).Return(nil).Once()

	require.NoError(t, h.svc.ResolveIntent(context.Background(), kyc.IntentResolution{
		ExternalID: intent.ExternalID, Outcome: models.KycIntentOutcomeCancelled, Reason: "tampered",
	}))
}

// A reconciliation naming the customer the operator found links it the way
// the worker would have — link and row together, the held application
// cleared — then resolves the intent and asks the provider where it stands.
func TestResolveIntentReconciledLinksTheCustomer(t *testing.T) {
	h, intents := newRecoveryHarness(t)
	intent := manualReviewIntent()
	intents.EXPECT().FindByExternalID(intent.ExternalID).Return(intent, nil, nil).Twice()
	link := linkedIntegration("")
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(link, nil)
	row := deferredRow()
	row.KycStatus = models.KycStatusIncomplete
	row.RecordSubmissionFailure("submission_held", "held")
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		RunAndReturn(func(uint, string) (*models.CustomerProviderKyc, error) { copied := *row; return &copied, nil })
	var saved *models.CustomerProviderKyc
	h.kycRepo.EXPECT().SaveWithLink(testifymock.Anything, false, link, models.KycStatusIncomplete).
		RunAndReturn(func(r *models.CustomerProviderKyc, _ bool, _ *models.CustomerProviderIntegration, _ string) (bool, error) {
			saved = r
			copied := *r
			row = &copied
			return true, nil
		}).Once()
	intents.EXPECT().Resolve(intent.ExternalID, models.KycIntentOutcomeReconciled, "found it", testifymock.Anything).Return(nil).Once()
	h.adapter.EXPECT().RefreshKyc(testifymock.Anything, ramp.NewCustomerRef(7, strPtr("cus_bp"))).
		Return(&ramp.KycState{Status: models.KycStatusUnderReview}, nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusIncomplete).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.KycStatus == models.KycStatusUnderReview
	})).Once()

	require.NoError(t, h.svc.ResolveIntent(context.Background(), kyc.IntentResolution{
		ExternalID: intent.ExternalID, Outcome: models.KycIntentOutcomeReconciled, Reason: "found it", CustomerID: "cus_bp",
	}))

	require.Equal(t, "cus_bp", *link.ExternalCustomerID)
	require.Equal(t, models.IntegrationStatusActive, link.Status)
	require.NotNil(t, saved)
	require.Nil(t, saved.ApplicationDeferredAt, "the customer exists: nothing is held any more")
	require.Nil(t, saved.LastErrorCode, "the held failure is cleared")
}

// The same customer already linked is a reconciliation of what is there.
func TestResolveIntentReconciledWithTheSameCustomer(t *testing.T) {
	h, intents := newRecoveryHarness(t)
	intent := manualReviewIntent()
	intents.EXPECT().FindByExternalID(intent.ExternalID).Return(intent, nil, nil).Twice()
	link := linkedIntegration("cus_bp")
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(link, nil)
	row := kycRow(models.KycStatusUnderReview)
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		RunAndReturn(func(uint, string) (*models.CustomerProviderKyc, error) { copied := *row; return &copied, nil })
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusUnderReview).Return(true, nil).Twice()
	intents.EXPECT().Resolve(intent.ExternalID, models.KycIntentOutcomeReconciled, "same", testifymock.Anything).Return(nil).Once()
	h.adapter.EXPECT().RefreshKyc(testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{Status: models.KycStatusUnderReview}, nil).Once()

	require.NoError(t, h.svc.ResolveIntent(context.Background(), kyc.IntentResolution{
		ExternalID: intent.ExternalID, Outcome: models.KycIntentOutcomeReconciled, Reason: "same", CustomerID: "cus_bp",
	}))
	h.kycRepo.AssertNotCalled(t, "SaveWithLink", testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// Another customer already linked is not overwritten: the operator decides
// between the two first, and the intent stays held.
func TestResolveIntentReconciledRefusesAnotherCustomer(t *testing.T) {
	h, intents := newRecoveryHarness(t)
	intent := manualReviewIntent()
	intents.EXPECT().FindByExternalID(intent.ExternalID).Return(intent, nil, nil).Twice()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(linkedIntegration("cus_other"), nil).Once()

	err := h.svc.ResolveIntent(context.Background(), kyc.IntentResolution{
		ExternalID: intent.ExternalID, Outcome: models.KycIntentOutcomeReconciled, Reason: "found", CustomerID: "cus_bp",
	})

	require.ErrorIs(t, err, kyc.ErrIntentCustomerConflict)
	intents.AssertNotCalled(t, "Resolve", testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// A late worker replay linked its own customer between the read and the
// write: the write refuses to replace it, and so does the resolution.
func TestResolveIntentReconciledLosesTheLinkRace(t *testing.T) {
	h, intents := newRecoveryHarness(t)
	intent := manualReviewIntent()
	intents.EXPECT().FindByExternalID(intent.ExternalID).Return(intent, nil, nil).Twice()
	link := linkedIntegration("")
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(link, nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(deferredRow(), nil).Once()
	h.kycRepo.EXPECT().SaveWithLink(testifymock.Anything, false, link, testifymock.Anything).
		Return(false, fmt.Errorf("user provider kyc repo: save with link: %w", repositories.ErrLinkConflict)).Once()

	err := h.svc.ResolveIntent(context.Background(), kyc.IntentResolution{
		ExternalID: intent.ExternalID, Outcome: models.KycIntentOutcomeReconciled, Reason: "found", CustomerID: "cus_bp",
	})

	require.ErrorIs(t, err, kyc.ErrIntentCustomerConflict)
	require.Nil(t, link.ExternalCustomerID, "the refused write leaves the link as it was read")
	intents.AssertNotCalled(t, "Resolve", testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// Resolved by someone else between the first read and the link: nothing is
// linked for an intent no longer held.
func TestResolveIntentRechecksTheStateBeforeLinking(t *testing.T) {
	h, intents := newRecoveryHarness(t)
	intent := manualReviewIntent()
	intents.EXPECT().FindByExternalID(intent.ExternalID).Return(intent, nil, nil).Once()
	resolved := *intent
	resolved.State = models.KycIntentResolved
	intents.EXPECT().FindByExternalID(intent.ExternalID).Return(&resolved, nil, nil).Once()

	err := h.svc.ResolveIntent(context.Background(), kyc.IntentResolution{
		ExternalID: intent.ExternalID, Outcome: models.KycIntentOutcomeReconciled, Reason: "found", CustomerID: "cus_bp",
	})

	require.ErrorIs(t, err, repositories.ErrKycIntentNotResolvable)
	h.kycRepo.AssertNotCalled(t, "SaveWithLink", testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// --- scrub ------------------------------------------------------------------

func TestScrubIntentMaterialCountsWhatItCleared(t *testing.T) {
	h, intents := newRecoveryHarness(t)
	before := time.Now()
	intents.EXPECT().ScrubExpiredMaterial(testifymock.Anything, 100).
		RunAndReturn(func(now time.Time, _ int) (int, error) {
			require.WithinRange(t, now, before, time.Now())
			return 3, nil
		}).Once()

	count, err := h.svc.ScrubIntentMaterial(100)

	require.NoError(t, err)
	require.Equal(t, 3, count)
}

// --- retention --------------------------------------------------------------

// A retention outside a day and a year is a typo, not a policy: it falls back
// to the default rather than keeping material for an hour or forever.
func TestIntentMaterialRetentionClamp(t *testing.T) {
	for hours, want := range map[int]time.Duration{
		0: 720 * time.Hour, -5: 720 * time.Hour, 23: 720 * time.Hour, 8761: 720 * time.Hour,
		24: 24 * time.Hour, 100: 100 * time.Hour, 8760: 8760 * time.Hour,
	} {
		require.Equal(t, want, kyc.IntentMaterialRetention(hours), "%d hours", hours)
	}
}

// The material of an intent held for review is kept for the configured
// retention.
func TestHeldIntentKeepsItsMaterialForTheConfiguredRetention(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	w.h, w.creator = newAtomicHarnessRetaining(t, "redis", w.store, 48*time.Hour)
	w.h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(w.link, nil).Maybe()
	w.h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		RunAndReturn(func(uint, string) (*models.CustomerProviderKyc, error) { row := *w.row; return &row, nil }).Maybe()
	intent := w.store.seed(models.KycIntentUnknown, repositories.KycIntentMaterial{
		Version: 1, SubmissionID: "stg_1", Prepared: atomicPrepared(),
	})
	intent.RetryDeadline = time.Now().Add(-time.Minute)
	intent.AttemptCount = 1
	w.staged(0)
	w.h.staging.EXPECT().Forget("stg_1").Once()
	saved := w.expectReleased()

	w.run(t)

	require.Equal(t, models.KycIntentManualReview, w.store.state())
	require.WithinDuration(t, time.Now().Add(48*time.Hour), *w.store.intent.MaterialRetainUntil, time.Minute)
	require.Equal(t, "submission_held", *(*saved).LastErrorCode)
}

func strPtr(s string) *string { return &s }
