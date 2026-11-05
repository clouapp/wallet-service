package kyc_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// atomicAdapter is a provider adapter that creates its customer atomically:
// the ordinary mock for everything a provider does, and the atomic port's mock
// for the four methods that make it one.
type atomicAdapter struct {
	*mocks.MockProviderAdapter
	atomic *mocks.MockAtomicKycCreator
}

func (a atomicAdapter) ValidateDeferredApplication(app ramp.Application) (*ramp.KycState, error) {
	return a.atomic.ValidateDeferredApplication(app)
}

func (a atomicAdapter) ValidateAtomicDocuments(docs ramp.Documents) error {
	return a.atomic.ValidateAtomicDocuments(docs)
}

func (a atomicAdapter) PrepareCustomer(ctx context.Context, submission ramp.AtomicKycSubmission,
	checkpoint ramp.AtomicKycCheckpoint, persist func(ramp.AtomicKycCheckpoint) error,
) (*ramp.PreparedAtomicKyc, error) {
	return a.atomic.PrepareCustomer(ctx, submission, checkpoint, persist)
}

func (a atomicAdapter) CreatePreparedCustomer(ctx context.Context, idempotencyKey string,
	prepared ramp.PreparedAtomicKyc,
) (*ramp.KycState, error) {
	return a.atomic.CreatePreparedCustomer(ctx, idempotencyKey, prepared)
}

var _ ramp.AtomicKycCreator = atomicAdapter{}

// newAtomicHarness is newHarness with the provider swapped for one that
// creates its customer atomically. Every other collaborator is the harness's.
func newAtomicHarness(t *testing.T) (*harness, *mocks.MockAtomicKycCreator) {
	t.Helper()
	return newAtomicHarnessWith(t, "sync", mocks.NewMockProviderKycSubmissionIntentRepository(t))
}

// newAtomicHarnessWith is newAtomicHarness on a queue driver and over an
// intent repository of the test's choosing.
func newAtomicHarnessWith(
	t *testing.T, queueDriver string, intents repositories.ProviderKycSubmissionIntentRepository,
) (*harness, *mocks.MockAtomicKycCreator) {
	t.Helper()
	return newAtomicHarnessRetaining(t, queueDriver, intents, 0)
}

// newAtomicHarnessRetaining is newAtomicHarnessWith keeping the material of an
// intent held for review for retention (zero is the default).
func newAtomicHarnessRetaining(
	t *testing.T, queueDriver string, intents repositories.ProviderKycSubmissionIntentRepository,
	retention time.Duration,
) (*harness, *mocks.MockAtomicKycCreator) {
	t.Helper()
	h := newHarnessWith(t, passthroughLocker{}, queueDriver)
	creator := mocks.NewMockAtomicKycCreator(t)
	h.svc = kyc.NewProviderKycService(kyc.Deps{
		Factory:      ramp.NewFactory(atomicAdapter{MockProviderAdapter: h.adapter, atomic: creator}),
		Config:       ramp.NewConfig(bridgeAloneEnabled(t)),
		Linker:       h.linker,
		Kyc:          h.kycRepo,
		Profiles:     h.profileRepo,
		Integrations: h.integRepo,
		Persons:      h.persons, Members: h.members,
		Staging:     h.staging,
		Dispatcher:  h.dispatcher,
		Locker:      passthroughLocker{},
		Broadcaster: h.broadcaster,
		Notifier:    h.notifier,
		Intents:     intents,
		// Retention of a held intent's material; zero takes the default.
		IntentMaterialRetention: retention,
	})
	return h, creator
}

// deferredState is what a provider that creates its customer atomically says
// an accepted application waits in: the documents it creates the customer with.
func deferredState() *ramp.KycState {
	return &ramp.KycState{
		Status:       models.KycStatusIncomplete,
		Requirements: []string{"government_id", "selfie"},
		Endorsements: []ramp.Endorsement{{Name: models.EndorsementPix, Status: "pending"}},
	}
}

// The provider cannot take the application without the documents, so the
// application is checked and held: nothing is staged, nothing is queued, and
// the row waits for the documents with the profile retained for the create.
func TestAtomicApplicationIsDeferred(t *testing.T) {
	h, creator := newAtomicHarnessWith(t, "sync", &intentStore{journal: new([]string)})
	h.adapter.EXPECT().RequiresTos().Return(true).Maybe()

	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	row := recordedTerms(kycRow(models.KycStatusNotStarted))
	row.RecordSubmissionFailure("tos_required", "an earlier refusal")
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	creator.EXPECT().ValidateDeferredApplication(testifymock.MatchedBy(func(app ramp.Application) bool {
		return app.SignedAgreementID == "sa_1" && app.Profile.Email == "luiza@example.com"
	})).Return(deferredState(), nil).Once()

	var retained ramp.Profile
	h.profileRepo.EXPECT().Upsert(uint(7), testifymock.Anything).
		RunAndReturn(func(_ uint, p ramp.Profile) error {
			retained = p
			return nil
		}).Once()
	var saved *models.CustomerProviderKyc
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusNotStarted).
		RunAndReturn(func(r *models.CustomerProviderKyc, _ string) (bool, error) {
			saved = r
			return true, nil
		}).Once()

	input := fullApplication()
	input.TaxID = "39053344705"
	input.Phone = "11987654321"
	input.Country = "BRA"
	got, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, input)
	require.NoError(t, err)

	h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
	h.dispatcher.AssertNotCalled(t, "Dispatch", testifymock.Anything, testifymock.Anything, testifymock.Anything)

	require.NotNil(t, saved)
	require.Same(t, saved, got)
	require.Equal(t, models.KycStatusIncomplete, saved.KycStatus)
	require.Equal(t, models.TosStatusApproved, saved.TosStatus)
	require.NotNil(t, saved.SignedAgreementID)
	require.Equal(t, "sa_1", *saved.SignedAgreementID)
	require.NotNil(t, saved.ApplicationDeferredAt)
	require.Equal(t, []string{"government_id", "selfie"}, saved.DecodedRequirements())
	require.Equal(t, []models.Endorsement{{Name: models.EndorsementPix, Status: "pending"}}, saved.DecodedEndorsements())
	require.Nil(t, saved.LastErrorCode, "an accepted application clears the last refusal")

	require.Equal(t, "Luiza", retained.FirstName)
	require.Equal(t, "39053344705", retained.TaxID)
	require.Equal(t, "+5511987654321", retained.Phone)
}

// recordedTerms is the row once the app reported the acceptance of the
// agreement fullApplication carries (RecordTosAcceptances).
func recordedTerms(row *models.CustomerProviderKyc) *models.CustomerProviderKyc {
	agreement := "sa_1"
	row.SignedAgreementID = &agreement
	row.TosStatus = models.TosStatusApproved
	return row
}

// The customer is born with the agreement and the row reads its terms as
// approved from then on, so an agreement the client names on the application
// without an acceptance this side recorded — none, or another one — is refused
// as no terms at all, and nothing is held.
func TestAtomicApplicationWithAnUnrecordedAgreementIsRefused(t *testing.T) {
	other := "sa_other"
	for name, recorded := range map[string]*string{"none recorded": nil, "another recorded": &other} {
		t.Run(name, func(t *testing.T) {
			h, creator := newAtomicHarness(t)
			h.adapter.EXPECT().RequiresTos().Return(true).Maybe()
			h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
				Return(linkedIntegration(""), nil).Once()
			row := kycRow(models.KycStatusNotStarted)
			row.SignedAgreementID = recorded
			h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
			h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusNotStarted).Return(true, nil).Once()

			_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())
			require.ErrorIs(t, err, ramp.ErrTosRequired)

			require.Equal(t, models.TosStatusPending, row.TosStatus)
			require.Nil(t, row.ApplicationDeferredAt)
			creator.AssertNotCalled(t, "ValidateDeferredApplication", testifymock.Anything)
			h.profileRepo.AssertNotCalled(t, "Upsert", testifymock.Anything, testifymock.Anything)
		})
	}
}

// The terms are refused before anything else, as for every provider.
func TestAtomicApplicationWithoutTermsIsRefused(t *testing.T) {
	h, creator := newAtomicHarness(t)
	h.adapter.EXPECT().RequiresTos().Return(true).Once()

	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusNotStarted), nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusNotStarted).Return(true, nil).Once()

	input := fullApplication()
	input.SignedAgreementID = ""
	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, input)
	require.ErrorIs(t, err, ramp.ErrTosRequired)

	creator.AssertNotCalled(t, "ValidateDeferredApplication", testifymock.Anything)
	h.profileRepo.AssertNotCalled(t, "Upsert", testifymock.Anything, testifymock.Anything)
	h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
}

// The person's one create is still active — in flight, or held for an
// operator: a new application would replace the one it is creating the
// customer from, so it is refused the way documents are, and nothing is held.
func TestAtomicApplicationWhileAnIntentIsActiveIsRefused(t *testing.T) {
	for state, want := range map[string]error{
		models.KycIntentUnknown:      kyc.ErrSubmissionInFlight,
		models.KycIntentManualReview: kyc.ErrSubmissionHeld,
	} {
		t.Run(state, func(t *testing.T) {
			store := &intentStore{journal: new([]string)}
			store.seed(state, repositories.KycIntentMaterial{Version: 1, SubmissionID: "stg_1"})
			h, creator := newAtomicHarnessWith(t, "sync", store)
			h.adapter.EXPECT().RequiresTos().Return(true).Maybe()
			h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
				Return(linkedIntegration(""), nil).Once()
			row := recordedTerms(kycRow(models.KycStatusIncomplete))
			h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
			creator.EXPECT().ValidateDeferredApplication(testifymock.Anything).Return(deferredState(), nil).Maybe()

			_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())
			require.ErrorIs(t, err, want)

			h.kycRepo.AssertNotCalled(t, "UpdateIfStatus", testifymock.Anything, testifymock.Anything)
			h.profileRepo.AssertNotCalled(t, "Upsert", testifymock.Anything, testifymock.Anything)
			h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
		})
	}
}

// An application the provider would refuse is a 422 now, with nothing held:
// no profile retained, no row marked as waiting for documents.
func TestAtomicApplicationValidationFailureStagesNothing(t *testing.T) {
	h, creator := newAtomicHarness(t)
	h.adapter.EXPECT().RequiresTos().Return(true).Maybe()

	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	row := recordedTerms(kycRow(models.KycStatusNotStarted))
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	creator.EXPECT().ValidateDeferredApplication(testifymock.Anything).
		Return(nil, fmt.Errorf("blindpay: tax id is required: %w", ramp.ErrKycDataInvalid)).Once()

	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())
	require.ErrorIs(t, err, ramp.ErrKycDataInvalid)

	require.Nil(t, row.ApplicationDeferredAt)
	h.kycRepo.AssertNotCalled(t, "UpdateIfStatus", testifymock.Anything, testifymock.Anything)
	h.profileRepo.AssertNotCalled(t, "Upsert", testifymock.Anything, testifymock.Anything)
	h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
	h.dispatcher.AssertNotCalled(t, "Dispatch", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// A held application is not owed again: the row has no customer yet, but it
// asks only for what the create still lacks.
func TestDeferredRowAsksOnlyForDocuments(t *testing.T) {
	deferredRow := func() *models.CustomerProviderKyc {
		row := kycRow(models.KycStatusIncomplete)
		row.SetRequirements(deferredState().Requirements)
		deferredAt := time.Now()
		row.ApplicationDeferredAt = &deferredAt
		return row
	}

	t.Run("get", func(t *testing.T) {
		h, _ := newAtomicHarness(t)
		h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(deferredRow(), nil).Once()
		h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
			Return(linkedIntegration(""), nil).Once()

		row, err := h.svc.Get(7, models.ProviderBridge)
		require.NoError(t, err)
		require.False(t, row.NeedsApplication)
		require.Equal(t, []string{"document", "selfie"}, kyc.PendingStepsFor(row))
	})

	t.Run("requirements", func(t *testing.T) {
		h, _ := newAtomicHarness(t)
		h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
			Return(linkedIntegration(""), nil).Once()
		h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(deferredRow(), nil).Once()

		got, err := h.svc.Requirements(context.Background(), 7, models.ProviderBridge)
		require.NoError(t, err)
		require.False(t, got.NeedsApplication)
		require.Equal(t, []string{"document", "selfie"}, got.PendingSteps)
	})
}

// Every other provider still stages the application and queues it.
func TestNonAtomicProvidersAreUntouched(t *testing.T) {
	h := newHarness(t)

	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusNotStarted), nil).Once()
	h.staging.EXPECT().Put(testifymock.MatchedBy(func(s kyc.StagedSubmission) bool {
		return s.Kind == kyc.KindApplication
	})).Return("stg_1", nil).Once()
	var saved *models.CustomerProviderKyc
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusNotStarted).
		RunAndReturn(func(r *models.CustomerProviderKyc, _ string) (bool, error) {
			saved = r
			return true, nil
		}).Once()
	h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).Return(nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusSubmitting), nil).Once()

	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())
	require.NoError(t, err)
	require.Equal(t, models.KycStatusSubmitting, saved.KycStatus)
	require.Nil(t, saved.ApplicationDeferredAt)
	h.profileRepo.AssertNotCalled(t, "Upsert", testifymock.Anything, testifymock.Anything)
}
