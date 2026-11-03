package kyc_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/goravel/framework/database/orm"
	"github.com/goravel/framework/support/carbon"
	"github.com/goravel/framework/testing/mock"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/mutualpay"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	"github.com/macrowallets/waas/packages/rampengine/app/services/locking"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// passthroughLocker runs fn immediately. Real lock semantics are covered by
// the AdvisoryLocker integration test.
type passthroughLocker struct{}

func (passthroughLocker) WithLock(ctx context.Context, _ string, fn func(context.Context) error) error {
	return fn(ctx)
}

// trackingLocker records whether WithLock is currently held.
type trackingLocker struct {
	held bool
}

func (l *trackingLocker) WithLock(ctx context.Context, _ string, fn func(context.Context) error) error {
	l.held = true
	defer func() { l.held = false }()
	return fn(ctx)
}

// harness bundles the mocks a test needs to assert on. The service takes nine
// collaborators because it is the orchestration point between the adapter, two
// tables, the queue and the event stream — the test keeps them addressable
// rather than hiding them behind a constructor.
type harness struct {
	svc *kyc.ProviderKycService
	// user is who sends what the tests submit: user 7, acting for its
	// individual customer, 7.
	user        *models.User
	persons     ramp.Persons
	members     *mocks.MockCustomerMemberRepository
	adapter     *mocks.MockProviderAdapter
	kycRepo     *mocks.MockCustomerProviderKycRepository
	profileRepo *mocks.MockCustomerIndividualProfileRepository
	retention   *retention
	integRepo   *mocks.MockCustomerProviderIntegrationRepository
	staging     *mocks.MockStaging
	dispatcher  *mocks.MockDispatcher
	linker      *mocks.MockLinker
	broadcaster *mocks.MockBroadcaster
	notifier    *mocks.MockNotifier
	// verdicts is the KYC statuses the devices were told about, in order.
	verdicts []string
}

// recordingNotifier records into verdicts each KYC status the service hands
// the devices; the notifier decides for itself which become a notification.
func recordingNotifier(t *testing.T, verdicts *[]string) *mocks.MockNotifier {
	t.Helper()
	notifier := mocks.NewMockNotifier(t)
	notifier.EXPECT().KycUpdated(testifymock.Anything, testifymock.Anything).Run(func(_ string, row *models.CustomerProviderKyc) {
		*verdicts = append(*verdicts, row.KycStatus)
	}).Maybe()
	return notifier
}

func newHarness(t *testing.T) *harness {
	return newHarnessWithLocker(t, passthroughLocker{})
}

// The queue driver decides whether a worker can hand work back to the queue.
// Under `sync` there is no worker and no later, so the paths that re-queue
// have to be exercised against a real driver name.
func newHarnessWithQueue(t *testing.T, driver string) *harness {
	return newHarnessWith(t, passthroughLocker{}, driver)
}

func linkedIntegration(externalID string) *models.CustomerProviderIntegration {
	row := &models.CustomerProviderIntegration{
		CustomerID: 7, Provider: models.ProviderBridge, Status: models.IntegrationStatusActive, Metadata: "{}",
	}
	if externalID != "" {
		row.ExternalCustomerID = &externalID
	}
	return row
}

func kycRow(status string) *models.CustomerProviderKyc {
	return &models.CustomerProviderKyc{
		// A row that came back from the repository has an id; the service reads
		// that to tell an insert from an update.
		Model:      orm.Model{ID: 42},
		CustomerID: 7, Provider: models.ProviderBridge, KycStatus: status,
		PreferredMode: models.KycModeAPIDocs, CollectionMode: models.KycModeAPIDocs,
		TosStatus: models.TosStatusPending,
		// The jsonb columns are NOT NULL.
		Endorsements: "[]", Requirements: "[]", RejectionReasons: "[]", RawStatus: "{}",
	}
}

func fullApplication() dto.ApplicationDTO {
	return dto.ApplicationDTO{
		SignedAgreementID: "sa_1",
		Endorsements:      []string{models.EndorsementPix},
		FirstName:         "Luiza",
		LastName:          "Souza",
		Email:             "luiza@example.com",
	}
}

// stagedApplication is what the worker half reads back from staging: already
// canonical, the shape fullApplication becomes once it is submitted.
func stagedApplication() ramp.Application {
	return ramp.Application{
		Type:              "individual",
		SignedAgreementID: "sa_1",
		Endorsements:      []string{models.EndorsementPix},
		Profile:           ramp.Profile{FirstName: "Luiza", LastName: "Souza", Email: "luiza@example.com"},
		LivenessCheckSelfies: []ramp.Selfie{
			{Image: "data:image/jpeg;base64,AAAA"},
		},
	}
}

// fullDocuments is fullApplication's counterpart for SubmitDocuments: a
// follow-up submission for a customer that already exists, so it carries no
// profile.
func fullDocuments() dto.DocumentsDTO {
	return dto.DocumentsDTO{DocumentType: "government_id", ImageFront: "data:image/jpeg;base64,AAAA"}
}

// stagedDocuments is stagedApplication's counterpart: what the worker half
// reads back from staging.
func stagedDocuments() ramp.Documents {
	return ramp.Documents{
		IdentifyingInformation: []ramp.IdentifyingDocument{
			{Type: "government_id", ImageFront: "data:image/jpeg;base64,AAAA"},
		},
	}
}

// sentBy is the account e-mail of the user sending the submission.
func (h *harness) sentBy(email string) { h.user.Email = email }

// sender is user 7 as the request hands them to the service.
func sender() *models.User {
	return &models.User{Model: orm.Model{ID: 7}, Email: "luiza@example.com"}
}

func newHarnessWithLocker(t *testing.T, locker locking.Locker) *harness {
	return newHarnessWith(t, locker, "sync")
}

func newHarnessWith(t *testing.T, locker locking.Locker, queueDriver string) *harness {
	t.Helper()
	mock.Factory().Log()
	h := &harness{
		user:        sender(),
		adapter:     mocks.NewMockProviderAdapter(t),
		kycRepo:     mocks.NewMockCustomerProviderKycRepository(t),
		profileRepo: mocks.NewMockCustomerIndividualProfileRepository(t),
		integRepo:   mocks.NewMockCustomerProviderIntegrationRepository(t),
		staging:     mocks.NewMockStaging(t),
		dispatcher:  mocks.NewMockDispatcher(t),
		linker:      mocks.NewMockLinker(t),
		broadcaster: mocks.NewMockBroadcaster(t),
	}
	h.notifier = recordingNotifier(t, &h.verdicts)
	h.adapter.EXPECT().Code().Return(models.ProviderBridge).Maybe()
	h.adapter.EXPECT().PreferredKycMode().Return(ramp.KycModeAPIDocs).Maybe()
	// No jurisdiction policy: the adapter serves whatever it supports, which
	// is everything (ramp.RailsOf).
	h.adapter.EXPECT().Supports(testifymock.Anything).Return(true).Maybe()
	h.dispatcher.EXPECT().RunsInline().Return(queueDriver == "sync").Maybe()
	h.retention = wireRetention(h.profileRepo)
	h.persons = ramp.Persons{Customers: support.IndividualCustomers(t), Profiles: h.profileRepo}
	h.members = support.IndividualMembers(t)
	// The application fan-out reads which providers are enabled before it
	// retains anything, so the settings answer Bridge alone, enabled, for the
	// tests that drive SubmitApplicationToEnabled this far. profileRepo.Upsert
	// carries no expectation unless a test sets one.
	h.svc = kyc.NewProviderKycService(kyc.Deps{
		Factory:  ramp.NewFactory(h.adapter),
		Config:   ramp.NewConfig(bridgeAloneEnabled(t)),
		Linker:   h.linker,
		Kyc:      h.kycRepo,
		Profiles: h.profileRepo,
		Persons:  h.persons, Members: h.members,
		Integrations: h.integRepo,
		Staging:      h.staging,
		Dispatcher:   h.dispatcher,
		Locker:       locker,
		Broadcaster:  h.broadcaster,
		Notifier:     h.notifier,
	})
	return h
}

// bridgeAloneEnabled is a settings source with Bridge enabled in sandbox and
// every other provider off, answering however often it is asked.
func bridgeAloneEnabled(t *testing.T) *mocks.MockSettingsSource {
	settings := mocks.NewMockSettingsSource(t)
	for _, code := range ramp.AllProviderCodes() {
		group := ramp.ProviderGroup(code)
		settings.EXPECT().Bool(group, ramp.SettingEnabled).Return(code == models.ProviderBridge, nil).Maybe()
		settings.EXPECT().String(group, ramp.SettingMode).Return(models.ProviderModeSandbox, nil).Maybe()
	}
	return settings
}

// --- submit: the request half -----------------------------------------------

// The submit is accepted, staged and queued. It must not reach the provider
// inside the request: the HTTP layer cancels at three seconds and the handler
// goroutine would finish with nobody left to persist the answer.
func TestSubmitApplicationStagesAndQueues(t *testing.T) {
	h := newHarness(t)

	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusNotStarted), nil).Once()
	h.staging.EXPECT().Put(testifymock.MatchedBy(func(s kyc.StagedSubmission) bool {
		return s.Kind == kyc.KindApplication && s.UserID == 7 &&
			s.Application != nil && s.PreviousStatus == models.KycStatusNotStarted
	})).Return("stg_1", nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).Return(nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusSubmitting), nil).Once()

	row, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())
	require.NoError(t, err)
	require.Equal(t, models.KycStatusSubmitting, row.KycStatus)
	h.adapter.AssertNotCalled(t, "SubmitApplication",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// What is staged is what every adapter reads, so it must already be the
// canonical application: adapters rely on the phone being E.164 and on the
// tax id arriving as a typed document instead of each reading the raw input.
func TestSubmitApplicationStagesTheCanonicalApplication(t *testing.T) {
	h := newHarness(t)

	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusNotStarted), nil).Once()
	var staged ramp.Application
	h.staging.EXPECT().Put(testifymock.Anything).
		RunAndReturn(func(s kyc.StagedSubmission) (string, error) {
			staged = *s.Application
			return "stg_1", nil
		}).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).Return(nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusSubmitting), nil).Once()

	app := fullApplication()
	app.Phone = "11987654321"
	app.Country = "BRA"
	app.TaxID = "39053344705"
	app.MotherName = "  Maria Souza "
	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, app)
	require.NoError(t, err)

	require.Equal(t, "+5511987654321", staged.Profile.Phone)
	require.Equal(t, "Maria Souza", staged.Profile.MotherName)
	require.Equal(t, []ramp.IdentifyingDocument{
		{Type: "cpf", IssuingCountry: "BRA", Number: "39053344705"},
	}, staged.IdentifyingInformation)
}

// Dispatch must not run while the advisory lock is held: with the sync driver
// Bridge HTTP would execute inside an open Postgres transaction.
func TestSubmitApplicationDispatchesOutsideLock(t *testing.T) {
	locker := &trackingLocker{}
	h := newHarnessWithLocker(t, locker)

	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusNotStarted), nil).Once()
	h.staging.EXPECT().Put(testifymock.Anything).Return("stg_1", nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).
		Run(func(string, uint, string) {
			require.False(t, locker.held, "Dispatch must run after advisory lock is released")
		}).Return(nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusSubmitting), nil).Once()

	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())
	require.NoError(t, err)
}

func TestSubmitApplicationFillsSignedAgreementFromRow(t *testing.T) {
	h := newHarness(t)

	row := kycRow(models.KycStatusNotStarted)
	agreement := "sa_persisted"
	row.SignedAgreementID = &agreement

	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	h.staging.EXPECT().Put(testifymock.MatchedBy(func(s kyc.StagedSubmission) bool {
		return s.Application.SignedAgreementID == "sa_persisted"
	})).Return("stg_1", nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).Return(nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(row, nil).Once()

	app := fullApplication()
	app.SignedAgreementID = ""
	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, app)
	require.NoError(t, err)
}

func TestSubmitApplicationPersistsSignedAgreementFromClient(t *testing.T) {
	h := newHarness(t)

	row := kycRow(models.KycStatusNotStarted)
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	h.staging.EXPECT().Put(testifymock.Anything).Return("stg_1", nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.MatchedBy(func(row *models.CustomerProviderKyc) bool {
		return row.SignedAgreementID != nil && *row.SignedAgreementID == "sa_client" &&
			row.TosStatus == models.TosStatusPending
	}), testifymock.Anything).Return(true, nil).Once()
	h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).Return(nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(row, nil).Once()

	app := fullApplication()
	app.SignedAgreementID = "sa_client"
	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, app)
	require.NoError(t, err)
}

// A client-supplied agreement id is staged for Bridge but must not mark ToS
// approved locally until the provider's customer response confirms acceptance.
func TestSubmitApplicationDoesNotApproveTosBeforeBridge(t *testing.T) {
	h := newHarness(t)

	row := kycRow(models.KycStatusNotStarted)
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	h.staging.EXPECT().Put(testifymock.Anything).Return("stg_1", nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.MatchedBy(func(row *models.CustomerProviderKyc) bool {
		return row.TosStatus == models.TosStatusPending
	}), testifymock.Anything).Return(true, nil).Once()
	h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).Return(nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(row, nil).Once()

	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())
	require.NoError(t, err)
}

// After an inline (sync) worker finishes, the handler must answer with the row
// re-read from the database, not the pre-worker in-memory snapshot.
func TestSubmitApplicationReturnsReloadedRowAfterInlineWorker(t *testing.T) {
	h := newHarness(t)

	reloaded := kycRow(models.KycStatusIncomplete)
	reloaded.Requirements = `["government_id_document"]`

	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusNotStarted), nil).Once()
	h.staging.EXPECT().Put(testifymock.Anything).Return("stg_1", nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).Return(nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(reloaded, nil).Once()

	row, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())
	require.NoError(t, err)
	require.Equal(t, models.KycStatusIncomplete, row.KycStatus)
	require.Equal(t, `["government_id_document"]`, row.Requirements)
}

// Nothing is staged when there is no agreement anywhere: the provider would
// reject it, and PII should not be written to Redis to find that out.
//
// The refusal is written to the row on the way out. It is the only one that
// happens before staging, so no release path will ever record it, and the
// client does not stop here — it sends its documents next, which the worker
// drops and releases to `incomplete` without adding a failure of its own,
// because this one is supposed to be there already.
func TestSubmitApplicationRequiresSignedAgreement(t *testing.T) {
	h := newHarness(t)

	h.adapter.EXPECT().RequiresTos().Return(true).Once()
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusNotStarted), nil).Once()

	var saved *models.CustomerProviderKyc
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(row *models.CustomerProviderKyc, _ string) (bool, error) {
			saved = row
			return true, nil
		}).Once()

	app := fullApplication()
	app.SignedAgreementID = ""
	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, app)
	require.ErrorIs(t, err, ramp.ErrTosRequired)
	h.staging.AssertNotCalled(t, "Put", testifymock.Anything)

	require.NotNil(t, saved, "the refusal has to reach the row, not only the response")
	require.NotNil(t, saved.LastErrorCode)
	require.Equal(t, "tos_required", *saved.LastErrorCode)
	// Untouched: the submission never left, so nothing about the compliance
	// state changed. Moving it here would invent progress the provider never saw.
	require.Equal(t, models.KycStatusNotStarted, saved.KycStatus)
}

// A second tap while one submission is on its way is not a failure, so nothing
// is written. Recording it would replace the refusal the user actually needs to
// read with a duplicate they did not make.
func TestSubmitApplicationDoesNotRecordAnInFlightRefusal(t *testing.T) {
	h := newHarness(t)

	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusSubmitting), nil).Once()

	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())
	require.ErrorIs(t, err, kyc.ErrSubmissionInFlight)
	h.kycRepo.AssertNotCalled(t, "Update", testifymock.Anything)
	h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
}

// The submit gate exists to spare the provider a rejection it would certainly
// make. A provider that issues no agreement id can never satisfy it, so the
// gate has to ask the provider first.
func TestSubmitApplicationDoesNotDemandAgreementFromProviderWithoutTerms(t *testing.T) {
	h := newHarness(t)

	row := kycRow(models.KycStatusNotStarted)
	h.adapter.EXPECT().RequiresTos().Return(false).Once()
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	h.staging.EXPECT().Put(testifymock.Anything).Return("stg_1", nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).Return(nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()

	app := fullApplication()
	app.SignedAgreementID = "" // the provider issues none

	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, app)
	require.NoError(t, err)
}

// An empty agreement id must leave the column NULL, not a pointer to "". NULL
// is what "no agreement" means to every other reader of the row; a pointer to
// an empty string would say something was signed.
func TestSubmitApplicationLeavesSignedAgreementNilForProviderWithoutTerms(t *testing.T) {
	h := newHarness(t)

	row := kycRow(models.KycStatusNotStarted)
	h.adapter.EXPECT().RequiresTos().Return(false).Once()
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	h.staging.EXPECT().Put(testifymock.Anything).Return("stg_1", nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.MatchedBy(func(row *models.CustomerProviderKyc) bool {
		return row.SignedAgreementID == nil
	}), testifymock.Anything).Return(true, nil).Once()
	h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).Return(nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()

	app := fullApplication()
	app.SignedAgreementID = "" // the provider issues none

	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, app)
	require.NoError(t, err)
}

// Each submission derives its own idempotency key, so two accepted submits
// would create two customers upstream. The in-flight guard is what stops a
// double tap from doing that.
func TestSubmitApplicationRejectsConcurrentSubmission(t *testing.T) {
	h := newHarness(t)

	inFlight := kycRow(models.KycStatusSubmitting)
	inFlight.UpdatedAt = carbon.NewDateTime(carbon.Now())

	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(inFlight, nil).Once()

	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())
	require.ErrorIs(t, err, kyc.ErrSubmissionInFlight)
	h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
}

// The other half of the same question. Passing every documents submission
// through did not narrow the duplicate check to the chaining case — it removed
// it: a double tap staged a second encrypted blob and dispatched a second job
// under its own idempotency key, so the provider took two independent uploads
// for one person and restarted its review on the second.
// A body the documents cannot be built from is refused before anything else
// runs: no integration is started, no row is read, nothing is staged. The
// harness's mocks carry no expectations, so any call fails the test.
func TestSubmitDocumentsRefusesAnIncompleteBodyBeforeTouchingAnything(t *testing.T) {
	cases := []struct {
		name  string
		input dto.DocumentsDTO
		want  error
	}{
		{"no file", dto.DocumentsDTO{DocumentType: "passport"}, kyc.ErrDocumentsRequired},
		{"an identity image with no type", dto.DocumentsDTO{ImageFront: "data:x"}, kyc.ErrDocumentTypeRequired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)

			_, err := h.svc.SubmitDocuments(context.Background(), h.user, 7, models.ProviderBridge, c.input)
			require.ErrorIs(t, err, c.want)
		})
	}
}

// The fan-out refuses once, before a single provider is listed. Refusing per
// provider instead would come back as outcomes, and the client would read a
// per-provider list where it expects the one code it can name.
func TestSubmitDocumentsToEnabledRefusesAnIncompleteBodyBeforeAnyProvider(t *testing.T) {
	cases := []struct {
		name  string
		input dto.DocumentsDTO
		want  error
	}{
		{"no file", dto.DocumentsDTO{DocumentType: "passport"}, kyc.ErrDocumentsRequired},
		{"an identity image with no type", dto.DocumentsDTO{ImageFront: "data:x"}, kyc.ErrDocumentTypeRequired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)

			outcomes, err := h.svc.SubmitDocumentsToEnabled(context.Background(), h.user, 7, c.input)
			require.ErrorIs(t, err, c.want)
			require.Empty(t, outcomes)
		})
	}
}

func TestSubmitDocumentsRefusesASecondDocumentsSubmission(t *testing.T) {
	h := newHarness(t)

	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(inFlightRow(models.ProviderBridge), nil).Once()
	h.staging.EXPECT().InFlightKind(uint(7), models.ProviderBridge).
		Return(kyc.KindDocuments).Once()

	_, err := h.svc.SubmitDocuments(context.Background(), h.user, 7, models.ProviderBridge,
		dto.DocumentsDTO{Selfie: "data:x"})
	require.ErrorIs(t, err, kyc.ErrSubmissionInFlight)
	h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
}

// A mark that Redis lost must not cost the submission. Refusing on an absent
// mark would put the client back where it started: polling for a customer
// before daring to send the documents it is already holding.
func TestSubmitDocumentsProceedsWhenTheInFlightMarkIsGone(t *testing.T) {
	h := newHarness(t)

	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(inFlightRow(models.ProviderBridge), nil).Once()
	h.staging.EXPECT().InFlightKind(uint(7), models.ProviderBridge).Return("").Once()
	h.staging.EXPECT().Put(testifymock.Anything).Return("stg_1", nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).Return(nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRowForProvider(models.ProviderBridge, models.KycStatusSubmitting), nil).Once()

	_, err := h.svc.SubmitDocuments(context.Background(), h.user, 7, models.ProviderBridge,
		dto.DocumentsDTO{Selfie: "data:x"})
	require.NoError(t, err)
}

// A worker that never ran would otherwise wedge the user on `submitting`
// forever. Past the staging TTL the payload cannot exist any more, so the
// earlier submission provably cannot complete and a new one is allowed.
func TestSubmitApplicationAllowsRetryAfterStagingExpired(t *testing.T) {
	h := newHarness(t)

	stale := kycRow(models.KycStatusSubmitting)
	stale.UpdatedAt = carbon.NewDateTime(carbon.Now().SubDuration((kyc.StagingTTL + time.Minute).String()))

	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(stale, nil).Once()
	h.staging.EXPECT().Put(testifymock.Anything).Return("stg_2", nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.dispatcher.EXPECT().Dispatch("stg_2", uint(7), models.ProviderBridge).Return(nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusSubmitting), nil).Once()

	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())
	require.NoError(t, err)
}

// The application email must match the authenticated user. Submitting another
// address would let an attacker trigger duplicate-email recovery for a victim.
func TestSubmitApplicationRejectsEmailMismatch(t *testing.T) {
	h := newHarness(t)

	h.sentBy("u@xip.io")

	app := fullApplication()
	app.Email = "victim@other.com"
	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, app)
	require.ErrorIs(t, err, kyc.ErrEmailMismatch)
	require.Equal(t, "kyc: email mismatch", err.Error())
	h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
	h.kycRepo.AssertNotCalled(t, "FindByCustomerAndProvider", testifymock.Anything, testifymock.Anything)
	// The refusal now precedes the side effect: no integration row is opened
	// for an application that was never going to be accepted.
	h.linker.AssertNotCalled(t, "Start", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// The declaration is only retained once it is known to be the account's own.
//
// Retaining first wrote an email the account does not own onto models.KycProfile
// — and that profile is exactly what pre-fills the wizard when a provider is
// added later, so the bad value outlived the request that was refused. The
// integration rows went the same way: linker.Start ran at every provider before
// anything was checked, so a refused request still left N+1 rows behind.
//
// The refusal itself is unchanged and must stay so: one outcome per provider,
// each carrying ErrEmailMismatch, is what keeps the controller on the
// fan-out branch that answers {"submissions":[...],"error":"email_mismatch"}.
// An early return here would empty that list and drop the key entirely.
func TestSubmitApplicationToEnabledDoesNotRetainAProfileTheAccountDoesNotOwn(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.seedProviderStates()

	// Read once for the whole fan-out, not once per provider.

	app := fullApplication()
	app.Email = "victim@other.com"

	outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7), app)
	require.ErrorIs(t, err, kyc.ErrEmailMismatch)
	require.Len(t, outcomes, 2, "one outcome per provider, or the client loses the submissions key")
	for _, outcome := range outcomes {
		require.ErrorIs(t, outcome.Err, kyc.ErrEmailMismatch)
	}
	h.profileRepo.AssertNotCalled(t, "Upsert", testifymock.Anything, testifymock.Anything)
	h.linker.AssertNotCalled(t, "Start", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
}

// --- submit: fan-out ---------------------------------------------------------

// kycRowForProvider is kycRow with an explicit provider, for fan-out tests
// that submit to more than Bridge in the same test.
func kycRowForProvider(provider, status string) *models.CustomerProviderKyc {
	row := kycRow(status)
	row.Provider = provider
	return row
}

// fanOutHarness is newHarnessWithLocker's multi-provider counterpart. The
// single-provider harness above hardcodes Bridge as the only registered
// adapter; the fan-out needs more than one, plus a config backed by settings
// instead of nothing.
type fanOutHarness struct {
	svc      *kyc.ProviderKycService
	user     *models.User
	persons  ramp.Persons
	members  *mocks.MockCustomerMemberRepository
	adapters map[string]*mocks.MockProviderAdapter
	// config is kept so a test can rebuild the service over the same enabled
	// set — what the attesting-provider cases need, since a composite adapter
	// cannot be registered by the loop above.
	config      *ramp.Config
	kycRepo     *mocks.MockCustomerProviderKycRepository
	profileRepo *mocks.MockCustomerIndividualProfileRepository
	retention   *retention
	integRepo   *mocks.MockCustomerProviderIntegrationRepository
	staging     *mocks.MockStaging
	dispatcher  *mocks.MockDispatcher
	linker      *mocks.MockLinker
	broadcaster *mocks.MockBroadcaster
	notifier    *mocks.MockNotifier
	verdicts    []string
	// registered is the adapter set the factory was built from, kept so a test
	// can rebuild the service with a different collaborator.
	registered []ramp.ProviderAdapter
}

// newFanOutHarness enables exactly the given providers: each gets its own
// registered, non-stub adapter, and the settings source answers for every
// code in ramp.AllProviderCodes() so Config.EligibleProviders() sees the same
// set the factory does.
func newFanOutHarness(t *testing.T, enabled ...string) *fanOutHarness {
	t.Helper()
	mock.Factory().Log()

	isEnabled := make(map[string]bool, len(enabled))
	for _, code := range enabled {
		isEnabled[code] = true
	}

	adapters := make(map[string]*mocks.MockProviderAdapter, len(enabled))
	registered := make([]ramp.ProviderAdapter, 0, len(enabled))
	settingsSource := mocks.NewMockSettingsSource(t)
	for _, code := range ramp.AllProviderCodes() {
		group := ramp.ProviderGroup(code)
		// Config.Provider always reads both fields, enabled or not, so every
		// code in the closed set needs an answer here.
		settingsSource.EXPECT().Bool(group, ramp.SettingEnabled).Return(isEnabled[code], nil).Once()
		settingsSource.EXPECT().String(group, ramp.SettingMode).Return(models.ProviderModeSandbox, nil).Once()
		if !isEnabled[code] {
			continue
		}
		adapter := mocks.NewMockProviderAdapter(t)
		adapter.EXPECT().Code().Return(code).Maybe()
		adapter.EXPECT().PreferredKycMode().Return(ramp.KycModeAPIDocs).Maybe()
		// No jurisdiction policy: it serves everyone (withPolicies adds one).
		adapter.EXPECT().Supports(testifymock.Anything).Return(true).Maybe()
		adapters[code] = adapter
		registered = append(registered, adapter)
	}

	h := &fanOutHarness{
		user:        sender(),
		adapters:    adapters,
		registered:  registered,
		kycRepo:     mocks.NewMockCustomerProviderKycRepository(t),
		profileRepo: mocks.NewMockCustomerIndividualProfileRepository(t),
		integRepo:   mocks.NewMockCustomerProviderIntegrationRepository(t),
		staging:     mocks.NewMockStaging(t),
		dispatcher:  mocks.NewMockDispatcher(t),
		linker:      mocks.NewMockLinker(t),
		broadcaster: mocks.NewMockBroadcaster(t),
		config:      ramp.NewConfig(settingsSource),
	}
	h.notifier = recordingNotifier(t, &h.verdicts)
	h.dispatcher.EXPECT().RunsInline().Return(true).Maybe()
	h.retention = wireRetention(h.profileRepo)
	h.persons = ramp.Persons{Customers: support.IndividualCustomers(t), Profiles: h.profileRepo}
	h.members = support.IndividualMembers(t)
	h.svc = kyc.NewProviderKycService(kyc.Deps{
		Factory:  ramp.NewFactory(registered...),
		Config:   h.config,
		Linker:   h.linker,
		Kyc:      h.kycRepo,
		Profiles: h.profileRepo,
		Persons:  h.persons, Members: h.members,
		Integrations: h.integRepo,
		Staging:      h.staging,
		Dispatcher:   h.dispatcher,
		Locker:       passthroughLocker{},
		Broadcaster:  h.broadcaster,
		Notifier:     h.notifier,
	})
	return h
}

// expectAcceptedSubmit wires every mock this harness needs for provider to
// accept one SubmitApplication call inline — the same sequence
// TestSubmitApplicationStagesAndQueues asserts on Bridge alone, keyed by
// provider instead of hardcoding it.
func (h *fanOutHarness) expectAcceptedSubmit(provider, stagingID string) {
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), provider).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), provider).
		Return(kycRowForProvider(provider, models.KycStatusNotStarted), nil).Once()
	h.staging.EXPECT().Put(testifymock.MatchedBy(func(s kyc.StagedSubmission) bool {
		return s.Provider == provider && s.Kind == kyc.KindApplication
	})).Return(stagingID, nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.dispatcher.EXPECT().Dispatch(stagingID, uint(7), provider).Return(nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), provider).
		Return(kycRowForProvider(provider, models.KycStatusSubmitting), nil).Once()
}

// expectProfileUpsert wires the fan-out's retained-profile write for user 7.
// It is opt-in rather than a harness default: only SubmitApplicationToEnabled
// retains anything (SubmitDocumentsToEnabled carries no profile to write),
// and Once() here is itself an assertion — the exact behaviour
// TestSubmitApplicationToEnabledUpsertsTheProfileExactlyOnce names directly,
// but every other application-fan-out test above benefits from the same
// regression guard for free.
func (h *fanOutHarness) expectProfileUpsert() {
	h.profileRepo.EXPECT().Upsert(uint(7), testifymock.Anything).Return(nil).Once()
}

// expectAccountEmail wires the one user read an application fan-out makes.
//
// Once() for the whole fan-out, not once per provider: the email is a fact
// about the account, not about any provider, so it is read before the loop.
// Wiring it per provider is what hid the N+1 — two eligible providers meant
// two identical reads of the same row to compare the same string twice.
func (h *fanOutHarness) expectAccountEmail() {
}

// expectAcceptedDocumentsSubmit is expectAcceptedSubmit's SubmitDocuments
// counterpart. Documents update a customer that already exists, so — unlike
// the application path — the linked integration must already carry an
// external customer id, and there is no user lookup to satisfy an email
// check.
func (h *fanOutHarness) expectAcceptedDocumentsSubmit(provider, stagingID string) {
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), provider).
		Return(linkedIntegration("cus_"+provider), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), provider).
		Return(kycRowForProvider(provider, models.KycStatusNotStarted), nil).Once()
	h.staging.EXPECT().Put(testifymock.MatchedBy(func(s kyc.StagedSubmission) bool {
		return s.Provider == provider && s.Kind == kyc.KindDocuments
	})).Return(stagingID, nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.dispatcher.EXPECT().Dispatch(stagingID, uint(7), provider).Return(nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), provider).
		Return(kycRowForProvider(provider, models.KycStatusSubmitting), nil).Once()
}

// One form, two providers, two jobs. The staged payload is per provider
// because it carries that provider's PreviousStatus, and because Forget on the
// first job to finish would blind the others if they shared one blob.
func TestSubmitApplicationToEnabledStagesOnePayloadPerProvider(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.seedProviderStates()
	h.expectAccountEmail()
	h.expectProfileUpsert()
	h.expectAcceptedSubmit(models.ProviderBridge, "stg_bridge")
	h.expectAcceptedSubmit(models.ProviderOzav, "stg_ozav")

	outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7), fullApplication())
	require.NoError(t, err)
	require.Len(t, outcomes, 2)

	require.Equal(t, models.ProviderBridge, outcomes[0].Provider)
	require.True(t, outcomes[0].Accepted())
	require.NotNil(t, outcomes[0].Row)
	require.Equal(t, models.ProviderOzav, outcomes[1].Provider)
	require.True(t, outcomes[1].Accepted())
	require.NotNil(t, outcomes[1].Row)

	// staging.Put and dispatcher.Dispatch each carry a .Once() expectation per
	// provider above; the mock's cleanup would already fail the test on a
	// mismatch, but the counts are asserted directly too.
	h.staging.AssertNumberOfCalls(t, "Put", 2)
	h.dispatcher.AssertNumberOfCalls(t, "Dispatch", 2)
}

// TestSubmitApplicationToEnabledUpsertsTheProfileExactlyOnce is the retention
// boundary's other half — see CustomerIndividualProfileRepositorySuite for what never
// reaches the row. This is what proves *how often*: two providers here, one
// call to Upsert. Writing it inside SubmitApplication instead — the mistake
// the design spec calls out by name — would have made this a call per
// provider, and profileRepo's .Once() (set by expectProfileUpsert, called
// once for this whole test) would fail on the second one.
//
// The expectation matches the profile by exact value, not a substring check
// on marshaled JSON. That used to be the only tool available when Upsert took
// a JSON string; now that it takes models.KycProfile (ramp.Profile's own
// alias — see ports.go), retainProfile has no field to smuggle an image
// through even by mistake, so the thing worth proving is that the exact
// profile submitted is what reaches the repository — canonicalised, the
// phone already in E.164, not a same-shaped one.
func TestSubmitApplicationToEnabledUpsertsTheProfileExactlyOnce(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.seedProviderStates()
	h.expectAccountEmail()
	app := fullApplication()
	app.Phone = "11987654321"
	app.Country = "BRA"
	h.profileRepo.EXPECT().Upsert(uint(7), ramp.Profile{
		FirstName: "Luiza", LastName: "Souza", Email: "luiza@example.com",
		Phone: "+5511987654321", Address: ramp.Address{Country: "BRA"},
	}).Return(nil).Once()
	h.expectAcceptedSubmit(models.ProviderBridge, "stg_bridge")
	h.expectAcceptedSubmit(models.ProviderOzav, "stg_ozav")

	outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7), app)
	require.NoError(t, err)
	require.Len(t, outcomes, 2)

	h.profileRepo.AssertNumberOfCalls(t, "Upsert", 1)
}

// The retained profile is what reserves the CPF (its digest is written with
// it), so a submission whose profile could not be retained is not sent: it
// would reach the providers holding a CPF nothing reserves for this account.
// It was a convenience for the next provider once, and a failure here logged
// and went on; now it fails closed, like the identity check before it.
func TestSubmitApplicationToEnabledRefusesWhenTheProfileCannotBeRetained(t *testing.T) {
	h := newHarness(t)
	broken := errors.New("profile repo: connection reset")
	h.profileRepo.EXPECT().Upsert(uint(7), testifymock.Anything).Return(broken).Once()

	outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7), fullApplication())

	require.ErrorIs(t, err, broken)
	require.Empty(t, outcomes)
	h.linker.AssertNotCalled(t, "Start", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
}

// A provider refusing must not cost the user the one that accepted.
func TestSubmitApplicationToEnabledAcceptsWhenAtLeastOneProviderTakesIt(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.seedProviderStates()
	h.expectAccountEmail()
	h.expectProfileUpsert()
	h.expectAcceptedSubmit(models.ProviderBridge, "stg_bridge")

	linkErr := errors.New("ozav: link failed")
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderOzav).
		Return(nil, linkErr).Once()

	outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7), fullApplication())
	require.NoError(t, err, "one accepted provider must not surface as a failure")
	require.Len(t, outcomes, 2)

	require.Equal(t, models.ProviderBridge, outcomes[0].Provider)
	require.True(t, outcomes[0].Accepted())
	require.Equal(t, models.ProviderOzav, outcomes[1].Provider)
	require.False(t, outcomes[1].Accepted())
	require.ErrorIs(t, outcomes[1].Err, linkErr)
}

// Nothing accepted is a failure, and the error is the first provider's own —
// not a summary. A caller mapping it to a response needs the reason, the same
// way Router.ResolveWithCall returns the last error rather than joining them.
func TestSubmitApplicationToEnabledFailsWhenNoProviderAccepts(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.seedProviderStates()
	h.expectAccountEmail()
	// The profile is the user's own declaration, written before any provider
	// is even asked — it is retained even when every one of them refuses.
	h.expectProfileUpsert()

	bridgeErr := errors.New("bridge: down")
	ozavErr := errors.New("ozav: down")
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(nil, bridgeErr).Once()
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderOzav).
		Return(nil, ozavErr).Once()

	outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7), fullApplication())
	require.ErrorIs(t, err, bridgeErr, "the returned error must be the first provider's own, not a summary")
	require.Len(t, outcomes, 2)
	for _, outcome := range outcomes {
		require.False(t, outcome.Accepted())
	}
}

// With a single provider enabled the fan-out has to be indistinguishable from
// today's behaviour — same row, same job, same staging call.
func TestSubmitApplicationToEnabledWithOneProviderMatchesSingleSubmit(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	h.seedProviderStates()
	h.expectAccountEmail()
	h.expectProfileUpsert()
	h.expectAcceptedSubmit(models.ProviderBridge, "stg_bridge")

	outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7), fullApplication())
	require.NoError(t, err)
	require.Len(t, outcomes, 1)
	require.Equal(t, models.ProviderBridge, outcomes[0].Provider)
	require.True(t, outcomes[0].Accepted())
	require.Equal(t, models.KycStatusSubmitting, outcomes[0].Row.KycStatus)

	h.staging.AssertNumberOfCalls(t, "Put", 1)
	h.dispatcher.AssertNumberOfCalls(t, "Dispatch", 1)
}

// newFanOutHarnessWithEnabledStub builds a harness where Bridge is enabled
// and implemented, and MutualPay is enabled in settings but only a stub in
// the factory: an operator can enable a provider before its adapter ships.
// mutualpay.New() is the real stub adapter, not a MockProviderAdapter: it is
// the only thing that satisfies the factory's private `stub` interface, which
// is what IsImplemented actually checks.
//
// No mock expectation is set up for anything MutualPay-shaped — linker,
// staging, dispatcher, kycRepo. That is deliberate: if
// SubmitApplicationToEnabled's IsImplemented guard is ever removed, the loop
// reaches one of those calls for MutualPay with no matching expectation, and
// the mock panics mid-test instead of quietly submitting to a stub.
func newFanOutHarnessWithEnabledStub(t *testing.T) *fanOutHarness {
	t.Helper()
	mock.Factory().Log()

	settingsSource := mocks.NewMockSettingsSource(t)
	for _, code := range ramp.AllProviderCodes() {
		group := ramp.ProviderGroup(code)
		enabled := code == models.ProviderBridge || code == models.ProviderMutualPay
		settingsSource.EXPECT().Bool(group, ramp.SettingEnabled).Return(enabled, nil).Once()
		settingsSource.EXPECT().String(group, ramp.SettingMode).Return(models.ProviderModeSandbox, nil).Once()
	}

	bridge := mocks.NewMockProviderAdapter(t)
	bridge.EXPECT().Code().Return(models.ProviderBridge).Maybe()
	bridge.EXPECT().PreferredKycMode().Return(ramp.KycModeAPIDocs).Maybe()
	bridge.EXPECT().Supports(testifymock.Anything).Return(true).Maybe()

	h := &fanOutHarness{
		user:        sender(),
		adapters:    map[string]*mocks.MockProviderAdapter{models.ProviderBridge: bridge},
		kycRepo:     mocks.NewMockCustomerProviderKycRepository(t),
		profileRepo: mocks.NewMockCustomerIndividualProfileRepository(t),
		integRepo:   mocks.NewMockCustomerProviderIntegrationRepository(t),
		staging:     mocks.NewMockStaging(t),
		dispatcher:  mocks.NewMockDispatcher(t),
		linker:      mocks.NewMockLinker(t),
		broadcaster: mocks.NewMockBroadcaster(t),
	}
	h.notifier = recordingNotifier(t, &h.verdicts)
	h.dispatcher.EXPECT().RunsInline().Return(true).Maybe()
	h.retention = wireRetention(h.profileRepo)
	h.persons = ramp.Persons{Customers: support.IndividualCustomers(t), Profiles: h.profileRepo}
	h.members = support.IndividualMembers(t)
	h.svc = kyc.NewProviderKycService(kyc.Deps{
		Factory:  ramp.NewFactory(bridge, mutualpay.New()),
		Config:   ramp.NewConfig(settingsSource),
		Linker:   h.linker,
		Kyc:      h.kycRepo,
		Profiles: h.profileRepo,
		Persons:  h.persons, Members: h.members,
		Integrations: h.integRepo,
		Staging:      h.staging,
		Dispatcher:   h.dispatcher,
		Locker:       passthroughLocker{},
		Broadcaster:  h.broadcaster,
		Notifier:     h.notifier,
	})
	return h
}

// A provider can be enabled in settings before its adapter ships.
// IsImplemented is what keeps that from becoming a real submission to a stub:
// staging.Put would write identity documents to Redis, a customer_provider_kycs row
// would be created, and a job would be dispatched, all for a provider that can
// never process any of it.
func TestSubmitApplicationToEnabledSkipsEnabledButUnimplementedProvider(t *testing.T) {
	h := newFanOutHarnessWithEnabledStub(t)
	h.seedProviderStates()
	h.expectAccountEmail()
	h.expectProfileUpsert()
	h.expectAcceptedSubmit(models.ProviderBridge, "stg_bridge")

	outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7), fullApplication())
	require.NoError(t, err)
	require.Len(t, outcomes, 1, "the stub must not produce an outcome at all")
	require.Equal(t, models.ProviderBridge, outcomes[0].Provider)

	// Only Bridge's Put/Dispatch happened — expectAcceptedSubmit's matcher
	// requires provider == bridge, so a stray MutualPay call would either fail
	// to match (mock panics on the unmatched call site first) or, if it
	// somehow matched, would push this count past 1.
	h.staging.AssertNumberOfCalls(t, "Put", 1)
	h.dispatcher.AssertNumberOfCalls(t, "Dispatch", 1)
}

// SubmitDocumentsToEnabled's mirror of
// TestSubmitApplicationToEnabledStagesOnePayloadPerProvider: one set of
// documents, two providers, two jobs, each StagedSubmission.Kind ==
// KindDocuments rather than KindApplication. The decision logic itself
// (partial success, the stub skip, which error surfaces when nobody accepts)
// is not re-asserted here — submitToEnabled is the same function under both
// SubmitApplicationToEnabled and SubmitDocumentsToEnabled, and the
// application-side fan-out tests above already exercise it, including
// TestSubmitApplicationToEnabledSkipsEnabledButUnimplementedProvider for the
// enabled-but-unimplemented case.
func TestSubmitDocumentsToEnabledStagesOnePayloadPerProvider(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	// Both hold a customer and no stored row names anything, so nobody is
	// skipped. (A provider holding no customer is — see
	// TestSubmitDocumentsToEnabledSkipsAProviderWithNoCustomerAndNoApplication.)
	h.seedSnapshot(nil, models.ProviderBridge, models.ProviderOzav)
	h.expectAcceptedDocumentsSubmit(models.ProviderBridge, "stg_bridge")
	h.expectAcceptedDocumentsSubmit(models.ProviderOzav, "stg_ozav")

	outcomes, err := h.svc.SubmitDocumentsToEnabled(context.Background(), h.user, 7, fullDocuments())
	require.NoError(t, err)
	require.Len(t, outcomes, 2)

	require.Equal(t, models.ProviderBridge, outcomes[0].Provider)
	require.True(t, outcomes[0].Accepted())
	require.NotNil(t, outcomes[0].Row)
	require.Equal(t, models.ProviderOzav, outcomes[1].Provider)
	require.True(t, outcomes[1].Accepted())
	require.NotNil(t, outcomes[1].Row)

	h.staging.AssertNumberOfCalls(t, "Put", 2)
	h.dispatcher.AssertNumberOfCalls(t, "Dispatch", 2)
}

// The application worker is what creates the customer, and in the wizard the
// documents follow the application immediately. Refusing here did not remove
// the wait — it moved it to the client, which paid for it by re-uploading the
// images on every poll and staging a fresh encrypted blob each time.
func TestSubmitDocumentsQueuesWhenTheCustomerDoesNotExistYet(t *testing.T) {
	h := newHarness(t)

	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRowForProvider(models.ProviderBridge, models.KycStatusNotStarted), nil).Once()
	h.staging.EXPECT().Put(testifymock.MatchedBy(func(sub kyc.StagedSubmission) bool {
		return sub.Kind == kyc.KindDocuments
	})).Return("stg_1", nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).Return(nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRowForProvider(models.ProviderBridge, models.KycStatusSubmitting), nil).Once()

	row, err := h.svc.SubmitDocuments(context.Background(), h.user, 7, models.ProviderBridge,
		dto.DocumentsDTO{Selfie: "data:x"})
	require.NoError(t, err)
	require.Equal(t, models.KycStatusSubmitting, row.KycStatus)
}

// The normal wizard order: the application leaves the row submitting, and the
// documents go out right behind it. Bouncing them off guardInFlight is what
// forced the client to poll for sixty seconds before sending them at all.
func TestSubmitDocumentsChainsBehindAnInFlightApplication(t *testing.T) {
	h := newHarness(t)

	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(inFlightRow(models.ProviderBridge), nil).Once()
	h.staging.EXPECT().InFlightKind(uint(7), models.ProviderBridge).
		Return(kyc.KindApplication).Once()
	h.staging.EXPECT().Put(testifymock.Anything).Return("stg_1", nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).Return(nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRowForProvider(models.ProviderBridge, models.KycStatusSubmitting), nil).Once()

	_, err := h.svc.SubmitDocuments(context.Background(), h.user, 7, models.ProviderBridge,
		dto.DocumentsDTO{Selfie: "data:x"})
	require.NoError(t, err, "documents must queue behind the application, not bounce off it")
}

// guardInFlight keeps the job it was written for: a duplicate application, from
// a second device or a double tap, is still refused.
func TestSubmitApplicationStillRefusesAConcurrentApplication(t *testing.T) {
	h := newHarness(t)

	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(inFlightRow(models.ProviderBridge), nil).Once()

	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())
	require.ErrorIs(t, err, kyc.ErrSubmissionInFlight)
	h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
}

// inFlightRow is a row the application worker is still running against: marked
// submitting, and recent enough that the staleness escape does not apply.
func inFlightRow(provider string) *models.CustomerProviderKyc {
	row := kycRowForProvider(provider, models.KycStatusSubmitting)
	now := carbon.NewDateTime(carbon.Now())
	row.UpdatedAt = now
	return row
}

// --- submit: the worker half ------------------------------------------------

func TestProcessStagedSubmissionPersistsAndBroadcasts(t *testing.T) {
	h := newHarness(t)

	app := stagedApplication()
	integ := linkedIntegration("")
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindApplication,
		Application: &app, PreviousStatus: models.KycStatusNotStarted,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(integ, nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusSubmitting), nil).Once()

	// The submission id is the idempotency key: stable across attempts at this
	// submission, different from the next one.
	h.adapter.EXPECT().SubmitApplication(testifymock.Anything, ramp.CustomerRef{CustomerID: 7},
		testifymock.MatchedBy(func(a ramp.Application) bool {
			return a.IdempotencyKey == "kyc-app:stg_1"
		})).
		Return(&ramp.KycState{
			ExternalCustomerID: "cus_1",
			Status:             models.KycStatusUnderReview,
			TosStatus:          models.TosStatusApproved,
			Endorsements:       []models.Endorsement{{Name: models.EndorsementPix, Status: models.EndorsementStatusPending}},
			Raw:                []byte(`{"id":"cus_1","tax_id":"39053344705"}`),
		}, nil).Once()

	// Both writes go through one transaction: a customer linked upstream whose
	// KYC row does not record it is how the next call issues a second link.
	h.kycRepo.EXPECT().SaveWithLink(testifymock.Anything, false, integ, testifymock.Anything).Return(true, nil).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.KycStatus == models.KycStatusUnderReview && e.FailureReason == ""
	})).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
	require.NotNil(t, integ.ExternalCustomerID)
	require.Equal(t, "cus_1", *integ.ExternalCustomerID, "the customer the submit created is what later calls address")
}

// A customer the link already names is not re-linked: the row is the only
// write, and the link row is left as it was.
func TestProcessStagedSubmissionWritesOnlyTheRowForALinkedCustomer(t *testing.T) {
	h := newHarness(t)

	docs := stagedDocuments()
	integ := linkedIntegration("cus_1")
	row := kycRow(models.KycStatusSubmitting)
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindDocuments,
		Documents: &docs, PreviousStatus: models.KycStatusIncomplete,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(integ, nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	h.adapter.EXPECT().SubmitDocuments(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{ExternalCustomerID: "cus_1", Status: models.KycStatusUnderReview}, nil).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	h.kycRepo.EXPECT().UpdateIfStatus(row, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
	require.Equal(t, models.KycStatusUnderReview, row.KycStatus)
}

// A failed submission changed nothing upstream, so the compliance state is
// what it was. The user learns about the failure through the event, not through
// a status that claims a review is happening.
func TestProcessStagedSubmissionRestoresStatusOnFailure(t *testing.T) {
	h := newHarness(t)

	app := stagedApplication()
	persisted := kycRow(models.KycStatusSubmitting)
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindApplication,
		Application: &app, PreviousStatus: models.KycStatusIncomplete,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(persisted, nil).Once()
	h.adapter.EXPECT().SubmitApplication(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, errors.New("bridge: POST /customers: upstream rejected (invalid_parameters)")).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(persisted, testifymock.Anything).Return(true, nil).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.KycStatus == models.KycStatusIncomplete && e.FailureReason != ""
	})).Once()

	// The job reports success: it handled the failure completely, and a queue
	// retry would only replay it.
	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
	require.Equal(t, models.KycStatusIncomplete, persisted.KycStatus)
}

// A create the provider refused over who the applicant is — a tax id another
// account's customer holds — is its answer, not a failure to retry: the row
// becomes rejected with the adapter's categories instead of going back to
// not_started owing the whole application (users 14 and 21 in production).
// The failure is still recorded, for the admin and the request id.
func TestProcessStagedSubmissionRecordsARefusalAsARejection(t *testing.T) {
	h := newHarness(t)

	app := stagedApplication()
	persisted := kycRow(models.KycStatusSubmitting)
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindApplication,
		Application: &app, PreviousStatus: models.KycStatusNotStarted,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(persisted, nil).Once()
	h.adapter.EXPECT().SubmitApplication(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, &ramp.KycRefusal{
			Categories: []string{models.KycRejectionTaxIDInUse},
			Details:    []string{"CUSTOMER_ALREADY_EXISTS"},
			Err:        errors.New("ozav: POST /customers: upstream rejected (CUSTOMER_ALREADY_EXISTS)"),
		}).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(persisted, testifymock.Anything).Return(true, nil).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.KycStatus == models.KycStatusRejected
	})).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
	require.Equal(t, models.KycStatusRejected, persisted.KycStatus)
	require.Equal(t, []string{models.KycRejectionTaxIDInUse}, persisted.DecodedRejectionCategories())
	require.Equal(t, []string{"CUSTOMER_ALREADY_EXISTS"}, persisted.DecodedRejectionDetails())
	require.True(t, persisted.RejectionTerminal())
	require.NotNil(t, persisted.LastErrorDetail)
	require.Contains(t, *persisted.LastErrorDetail, "CUSTOMER_ALREADY_EXISTS")
	require.Equal(t, []string{models.KycStatusRejected}, h.verdicts,
		"the refusal arrives after the request answered, so the devices hear it")
}

// A stale retry captures `submitting` as its own previous status, because that
// is what the row said when guardInFlight let it through. Restoring it verbatim
// would weld the value to the row: every later failure would restore it again.
func TestProcessStagedSubmissionNeverRestoresSubmitting(t *testing.T) {
	h := newHarness(t)

	app := stagedApplication()
	persisted := kycRow(models.KycStatusSubmitting)
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindApplication,
		Application: &app, PreviousStatus: models.KycStatusSubmitting,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(persisted, nil).Once()
	h.adapter.EXPECT().SubmitApplication(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, errors.New("bridge: POST /customers: upstream rejected (invalid_parameters)")).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(persisted, testifymock.Anything).Return(true, nil).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
	require.Equal(t, models.KycStatusIncomplete, persisted.KycStatus)
}

// The payload is gone, so nothing can be sent — but the row it marked in flight
// still has to be released. Left on `submitting`, no event ever moves it and the
// app waits on an outcome that never arrives.
func TestProcessStagedSubmissionReleasesTheRowWhenStagingExpired(t *testing.T) {
	h := newHarness(t)

	row := kycRow(models.KycStatusSubmitting)
	h.staging.EXPECT().Get("stg_gone").Return(nil, kyc.ErrStagingExpired).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(row, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.KycStatus == models.KycStatusIncomplete && e.FailureReason != ""
	})).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_gone", 7, models.ProviderBridge),
		"an expired payload cannot be recovered by retrying")
	require.Equal(t, models.KycStatusIncomplete, row.KycStatus)
}

// Only a row actually in flight is released. A submission whose staging expired
// after the outcome already landed must not walk an approved row backwards.
func TestProcessStagedSubmissionLeavesASettledRowAlone(t *testing.T) {
	h := newHarness(t)

	row := kycRow(models.KycStatusApproved)
	h.staging.EXPECT().Get("stg_gone").Return(nil, kyc.ErrStagingExpired).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_gone", 7, models.ProviderBridge))
	require.Equal(t, models.KycStatusApproved, row.KycStatus)
	h.kycRepo.AssertNotCalled(t, "Update", testifymock.Anything)
	h.broadcaster.AssertNotCalled(t, "PublishKyc", testifymock.Anything, testifymock.Anything)
}

// --- hosted link ------------------------------------------------------------

func TestHostedLinkPersistsUrlAndBackfillsCustomer(t *testing.T) {
	h := newHarness(t)

	integ := linkedIntegration("")
	row := kycRow(models.KycStatusNotStarted)

	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).Return(integ, nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	h.adapter.EXPECT().CreateHostedKycLink(testifymock.Anything, testifymock.Anything,
		testifymock.MatchedBy(func(o ramp.HostedLinkOpts) bool {
			return o.FullName == "Luiza Souza" && o.Email == "luiza@example.com" && o.Reason == "api_submit_failed"
		})).
		Return(&ramp.HostedKycLink{
			LinkID: "kyc_link_1", URL: "https://bridge.xyz/kyc/abc",
			ExternalCustomerID: "cus_9", CollectionMode: ramp.CollectionModeHostedFallback,
		}, nil).Once()
	h.kycRepo.EXPECT().SaveWithLink(row, false, integ, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	got, err := h.svc.HostedLink(context.Background(), h.user, 7, models.ProviderBridge, dto.HostedLinkDTO{
		FullName: "Luiza Souza", Reason: "api_submit_failed",
	})
	require.NoError(t, err)
	require.NotNil(t, got.HostedKycURL)
	require.Equal(t, "https://bridge.xyz/kyc/abc", *got.HostedKycURL)
	require.Equal(t, models.KycModeHostedFallback, got.CollectionMode)
	require.Equal(t, "cus_9", *integ.ExternalCustomerID, "the hosted link is the other way a customer is born")
}

// An operator unlinking the provider while the page was being issued leaves
// the link gone: the write refuses rather than keep a row for a customer
// nothing links to, and the user is asked to start over.
func TestHostedLinkOfALinkUnlinkedMeanwhileAsksForTheIntegration(t *testing.T) {
	h := newHarness(t)

	integ := linkedIntegration("")
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).Return(integ, nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(nil, nil).Once()
	h.adapter.EXPECT().RequiresTos().Return(true).Maybe()
	h.adapter.EXPECT().CreateHostedKycLink(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.HostedKycLink{LinkID: "kyc_link_1", URL: "https://bridge.xyz/kyc/abc", ExternalCustomerID: "cus_9"}, nil).Once()
	h.kycRepo.EXPECT().SaveWithLink(testifymock.Anything, true, integ, testifymock.Anything).
		Return(false, fmt.Errorf("user provider kyc repo: save with link: %w", repositories.ErrIntegrationGone)).Once()

	got, err := h.svc.HostedLink(context.Background(), h.user, 7, models.ProviderBridge, dto.HostedLinkDTO{FullName: "Luiza Souza"})

	require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
	require.Nil(t, got)
}

// RequestEndorsements asks for a capability and keeps whatever compliance
// state the provider answers with — the endorsement list is what opens a rail,
// so a request that moved the customer must leave the row saying so.
func TestRequestEndorsementsAppliesAndBroadcasts(t *testing.T) {
	h := newHarness(t)

	integ := linkedIntegration("cus_1")
	row := kycRow(models.KycStatusUnderReview)

	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(integ, nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	h.adapter.EXPECT().RequestEndorsements(testifymock.Anything,
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, []string{models.EndorsementPix}).
		Return(&ramp.KycState{
			Status:       models.KycStatusApproved,
			Endorsements: []models.Endorsement{{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved}},
		}, nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(row, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	got, err := h.svc.RequestEndorsements(context.Background(), 7, models.ProviderBridge,
		[]string{models.EndorsementPix})
	require.NoError(t, err)
	require.Equal(t, models.KycStatusApproved, got.KycStatus)
	require.True(t, got.SupportsRail(models.RailPixPayin))
}

// The same rule Refresh applies: an unchanged status is not news, and
// publishing it would wake every idle app for nothing.
func TestRequestEndorsementsDoesNotBroadcastUnchangedStatus(t *testing.T) {
	h := newHarness(t)

	integ := linkedIntegration("cus_1")
	row := kycRow(models.KycStatusUnderReview)

	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(integ, nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	h.adapter.EXPECT().RequestEndorsements(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{Status: models.KycStatusUnderReview}, nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(row, testifymock.Anything).Return(true, nil).Once()

	_, err := h.svc.RequestEndorsements(context.Background(), 7, models.ProviderBridge, nil)
	require.NoError(t, err)
	h.broadcaster.AssertNotCalled(t, "PublishKyc", testifymock.Anything, testifymock.Anything)
}

// The upstream refusal travels out wrapped under this call's own noun, and
// nothing is persisted: the provider changed nothing, so neither does the row.
func TestRequestEndorsementsWrapsTheProviderRefusal(t *testing.T) {
	h := newHarness(t)

	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusUnderReview), nil).Once()
	h.adapter.EXPECT().RequestEndorsements(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, ramp.ErrProviderFailed).Once()

	_, err := h.svc.RequestEndorsements(context.Background(), 7, models.ProviderBridge,
		[]string{models.EndorsementPix})
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	require.Contains(t, err.Error(), "request endorsements at "+models.ProviderBridge)
}

// A user with no integration row has no customer to ask for anything, and the
// sentinel is what the controller maps to its own status.
func TestRequestEndorsementsRequiresAnIntegration(t *testing.T) {
	h := newHarness(t)

	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(nil, nil).Once()

	_, err := h.svc.RequestEndorsements(context.Background(), 7, models.ProviderBridge, nil)
	require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
}

// --- read paths -------------------------------------------------------------

func TestRefreshAppliesAndBroadcasts(t *testing.T) {
	h := newHarness(t)

	integ := linkedIntegration("cus_1")
	row := kycRow(models.KycStatusUnderReview)

	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(integ, nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	h.adapter.EXPECT().RefreshKyc(testifymock.Anything, ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}).
		Return(&ramp.KycState{
			Status:       models.KycStatusApproved,
			Endorsements: []models.Endorsement{{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved}},
			Raw:          []byte(`{"status":"active"}`),
		}, nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(row, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	got, err := h.svc.Refresh(context.Background(), 7, models.ProviderBridge)
	require.NoError(t, err)
	require.Equal(t, models.KycStatusApproved, got.KycStatus)
	require.True(t, got.SupportsRail(models.RailPixPayin))
	require.NotNil(t, got.LastSyncedAt)
	require.Equal(t, []string{models.KycStatusApproved}, h.verdicts, "the devices hear the verdict the sync found")
}

// A refresh that came back with the provider's own state forgets the last
// submission failure.
//
// The failure is a stand-in for information we did not have: the request
// answered 202 long before the worker learned the outcome, so the row carries
// the refusal because nothing else can. A sync is that information arriving —
// and an old refusal shown next to the provider's current answer reads as the
// current one, which is the whole reason ClearSubmissionFailure exists.
//
// Measured on a real account (02/09): a create that Ozav answered with 500 had
// in fact created the customer. The refresh read it back, requirements and all,
// and the stale provider_busy on the row still drove the app to "you were
// refused, start over".
func TestRefreshForgetsAFailureTheProviderStateContradicts(t *testing.T) {
	h := newHarness(t)

	integ := linkedIntegration("cus_1")
	row := kycRow(models.KycStatusIncomplete)
	row.RecordSubmissionFailure("provider_busy", "ozav: POST /customers: upstream rejected")

	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(integ, nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	h.adapter.EXPECT().RefreshKyc(testifymock.Anything, ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}).
		Return(&ramp.KycState{
			Status:       models.KycStatusIncomplete,
			Requirements: []string{"source_of_funds", "occupation"},
			Raw:          []byte(`{"status":"pending"}`),
		}, nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(row, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Maybe()

	got, err := h.svc.Refresh(context.Background(), 7, models.ProviderBridge)
	require.NoError(t, err)
	require.Nil(t, got.LastErrorCode, "the provider answered about this customer; the old refusal is not the current story")
	require.Nil(t, got.LastErrorDetail)
	require.Nil(t, got.LastErrorAt)
	// What the user still owes now travels in requirements, which is the
	// channel that can actually name it.
	require.Equal(t, `["source_of_funds","occupation"]`, got.Requirements)
}

// The categories the adapter mapped are persisted with the status, and the
// next answer replaces them: a customer rejected for a selfie and approved
// after resubmitting one is not still refused.
func TestRefreshPersistsAndReplacesTheRejectionCategories(t *testing.T) {
	h := newHarness(t)

	integ := linkedIntegration("cus_1")
	row := kycRow(models.KycStatusUnderReview)

	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(integ, nil).Twice()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Twice()
	h.adapter.EXPECT().RefreshKyc(testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{
			Status:              models.KycStatusRejected,
			RejectionCategories: []string{models.KycRejectionSelfieMismatch},
			RejectionDetails:    []string{"The customer's submitted ID photo did not match their live portrait."},
		}, nil).Once()
	h.adapter.EXPECT().RefreshKyc(testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{Status: models.KycStatusApproved}, nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(row, testifymock.Anything).Return(true, nil).Twice()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Twice()

	got, err := h.svc.Refresh(context.Background(), 7, models.ProviderBridge)
	require.NoError(t, err)
	require.Equal(t, []string{models.KycRejectionSelfieMismatch}, got.DecodedRejectionCategories())
	require.Len(t, got.DecodedRejectionDetails(), 1)

	got, err = h.svc.Refresh(context.Background(), 7, models.ProviderBridge)
	require.NoError(t, err)
	require.JSONEq(t, `[]`, got.RejectionCategories)
	require.JSONEq(t, `[]`, got.RejectionDetails)
}

// An unchanged status is not news. Publishing it anyway would wake every idle
// app on a reconciliation sweep.
func TestRefreshDoesNotBroadcastUnchangedStatus(t *testing.T) {
	h := newHarness(t)

	integ := linkedIntegration("cus_1")
	row := kycRow(models.KycStatusUnderReview)

	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(integ, nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	h.adapter.EXPECT().RefreshKyc(testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{Status: models.KycStatusUnderReview}, nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(row, testifymock.Anything).Return(true, nil).Once()

	_, err := h.svc.Refresh(context.Background(), 7, models.ProviderBridge)
	require.NoError(t, err)
	h.broadcaster.AssertNotCalled(t, "PublishKyc", testifymock.Anything, testifymock.Anything)
	require.Empty(t, h.verdicts)
}

// A user who has never touched a provider is not an error — the app needs a
// state to render, and nothing is written for a read.
func TestGetSynthesizesNotStarted(t *testing.T) {
	h := newHarness(t)

	h.adapter.EXPECT().RequiresTos().Return(true).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(nil, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(nil, nil).Once()

	got, err := h.svc.Get(7, models.ProviderBridge)
	require.NoError(t, err)
	require.True(t, got.NeedsApplication, "never touched, so it owes the application")
	require.Equal(t, models.KycStatusNotStarted, got.KycStatus)
	require.Equal(t, models.KycModeAPIDocs, got.PreferredMode)
	require.Equal(t, models.TosStatusPending, got.TosStatus)
	require.Zero(t, got.ID)
	h.kycRepo.AssertNotCalled(t, "Create", testifymock.Anything)
}

// A provider with no terms of service must synthesize `not_required`, not
// `pending`: nothing the user does will ever move a ToS status forward for a
// provider that never issues one, and `pending` reads as an outstanding step.
func TestGetSynthesizesNotRequiredTosForProviderWithoutTerms(t *testing.T) {
	h := newHarness(t)

	h.adapter.EXPECT().RequiresTos().Return(false).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(nil, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(nil, nil).Once()

	got, err := h.svc.Get(7, models.ProviderBridge)
	require.NoError(t, err)
	require.Equal(t, models.TosStatusNotRequired, got.TosStatus)
}

// A provider holding no customer has nothing to ask upstream — Bridge answers
// such a call with 409 — and owes the whole application. The per-provider
// wizard opens from this answer, so it has to be one.
func TestRequirementsWithoutACustomerOwesTheWholeApplication(t *testing.T) {
	for name, link := range map[string]*models.CustomerProviderIntegration{
		"no integration":           nil,
		"integration, no customer": linkedIntegration(""),
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(link, nil).Once()
			h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
				Return(kycRow(models.KycStatusIncomplete), nil).Once()

			got, err := h.svc.Requirements(context.Background(), 7, models.ProviderBridge)
			require.NoError(t, err)
			require.Equal(t, models.KycStatusIncomplete, got.Status)
			require.True(t, got.NeedsApplication)
			require.Equal(t, []string{"personal", "profile", "address", "document", "selfie"}, got.PendingSteps)
			h.adapter.AssertNotCalled(t, "GetRequirements", testifymock.Anything, testifymock.Anything)
		})
	}
}

// The provider's snapshot restates submitted customer fields. It belongs in
// raw_status for the admin and nowhere near a client response.
func TestRequirementsIncludesPendingSteps(t *testing.T) {
	h := newHarness(t)

	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	h.adapter.EXPECT().GetRequirements(testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycRequirements{
			Status:  models.KycStatusIncomplete,
			Missing: []string{"proof_of_address", "live_selfie"},
		}, nil).Once()

	got, err := h.svc.Requirements(context.Background(), 7, models.ProviderBridge)
	require.NoError(t, err)
	require.Equal(t, []string{"proof_of_address", "selfie"}, got.PendingSteps)
}

func TestRequirementsNeverExposesRawSnapshot(t *testing.T) {
	h := newHarness(t)

	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	h.adapter.EXPECT().GetRequirements(testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycRequirements{
			Status:  models.KycStatusIncomplete,
			Missing: []string{"proof_of_address"},
			Raw:     []byte(`{"tax_id":"39053344705"}`),
		}, nil).Once()

	got, err := h.svc.Requirements(context.Background(), 7, models.ProviderBridge)
	require.NoError(t, err)
	require.Equal(t, []string{"proof_of_address"}, got.Missing)

	// The view is what a controller serializes, so the check is on the wire
	// form: no field of it may carry the snapshot through.
	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "39053344705")
}

// --- start tos --------------------------------------------------------------

var tosIdempotencyKeyPattern = regexp.MustCompile(`^kyc-tos:7:bridge:[0-9a-f]{64}:\d{4}-\d{2}-\d{2}$`)

func expectStartTosMocks(h *harness, row *models.CustomerProviderKyc) {
	h.adapter.EXPECT().RequiresTos().Return(true).Once()
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	// Twice: once to probe the row for a session worth reusing, before any
	// upstream call, and once inside the lock for the read-modify-write.
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Twice()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, row.KycStatus).Return(true, nil).Once()
}

// The ToS idempotency key is server-derived from a normalized redirect hash,
// not the raw client bytes — cosmetic differences must not mint new sessions.
func TestStartTosDerivesStableIdempotencyKeyFromNormalizedRedirect(t *testing.T) {
	h := newHarness(t)
	row := kycRow(models.KycStatusNotStarted)
	tosURL := "https://bridge.xyz/tos"

	var keys []string
	h.adapter.EXPECT().CreateTosSession(testifymock.Anything, testifymock.Anything,
		testifymock.MatchedBy(func(opts ramp.TosOpts) bool {
			keys = append(keys, opts.IdempotencyKey)
			return tosIdempotencyKeyPattern.MatchString(opts.IdempotencyKey) &&
				!strings.Contains(opts.IdempotencyKey, "xip://")
		})).
		Return(&ramp.TosSession{URL: tosURL}, nil).Twice()

	expectStartTosMocks(h, row)
	_, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")
	require.NoError(t, err)

	expectStartTosMocks(h, row)
	_, err = h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "  XIP://KYC/tos  ")
	require.NoError(t, err)

	require.Len(t, keys, 2)
	require.Equal(t, keys[0], keys[1])
}

func TestStartTosUsesDifferentIdempotencyKeyForDifferentRedirect(t *testing.T) {
	h := newHarness(t)
	row := kycRow(models.KycStatusNotStarted)
	tosURL := "https://bridge.xyz/tos"

	var keys []string
	h.adapter.EXPECT().CreateTosSession(testifymock.Anything, testifymock.Anything,
		testifymock.MatchedBy(func(opts ramp.TosOpts) bool {
			keys = append(keys, opts.IdempotencyKey)
			return tosIdempotencyKeyPattern.MatchString(opts.IdempotencyKey)
		})).
		Return(&ramp.TosSession{URL: tosURL}, nil).Twice()

	expectStartTosMocks(h, row)
	_, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")
	require.NoError(t, err)

	expectStartTosMocks(h, row)
	_, err = h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/done")
	require.NoError(t, err)

	require.Len(t, keys, 2)
	require.NotEqual(t, keys[0], keys[1])
}

// A provider with no terms of service must not be asked for a ToS session at
// all. Calling and swallowing the error would still cost a round trip and
// would depend on the adapter returning exactly the right sentinel.
func TestStartTosSkipsProviderWithoutTerms(t *testing.T) {
	h := newHarness(t)

	h.adapter.EXPECT().RequiresTos().Return(false).Once()

	_, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "https://app.xip.io/done")

	require.ErrorIs(t, err, ramp.ErrTosNotRequired)
	// Nothing else may have run: no linker expectation is set, so a call to
	// linker.Start would fail the mock. That is the assertion — the check has
	// to come before the integration is started, not after.
	h.adapter.AssertNotCalled(t, "CreateTosSession", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// A stub provider (MutualPay, Ozav) also declares RequiresTos() == false, but
// answering `tos_not_required` there would be a lesser truth: the bigger
// problem is that the provider is not implemented at all, and the client
// would be sent on to hit a 503 on the very next call. The unimplemented
// check must win.
func TestStartTosRejectsStubProviderBeforeCheckingTerms(t *testing.T) {
	svc := kyc.NewProviderKycService(kyc.Deps{
		Factory: ramp.NewFactory(mutualpay.New()),
	})

	_, err := svc.StartTos(context.Background(), 7, models.ProviderMutualPay, "https://app.xip.io/done")

	require.ErrorIs(t, err, ramp.ErrProviderNotImplemented)
	require.NotErrorIs(t, err, ramp.ErrTosNotRequired)
}

func TestUnknownProviderIsRejectedEverywhere(t *testing.T) {
	h := newHarness(t)

	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), "nope", fullApplication())
	require.ErrorIs(t, err, ramp.ErrProviderNotImplemented)

	_, err = h.svc.Refresh(context.Background(), 7, "nope")
	require.ErrorIs(t, err, ramp.ErrProviderNotImplemented)

	_, err = h.svc.Requirements(context.Background(), 7, "nope")
	require.ErrorIs(t, err, ramp.ErrProviderNotImplemented)
}

// The skip condition is the same question the wizard asks the user — "is there
// anything left you can act on?" — and it has to be, because the client
// collects the union of the pending steps once and this decides who receives
// that collection. Asking a different question on each side is how an approved
// customer ends up receiving documents again and regressing to under_review.
func TestShouldSkipRedistribution(t *testing.T) {
	tests := []struct {
		name         string
		status       string
		requirements []string
		want         bool
	}{
		{"approved owing nothing", models.KycStatusApproved, nil, true},
		{
			// The naive-rule counter-example: Ozav's approved customers are
			// never requirement-free, since the app now collects the
			// risk-profile fields and they open the profile step. But a
			// document resubmission cannot fill in a profile field — only
			// SubmitApplicationToEnabled can — so DocumentSteps narrows this
			// to nothing, and the provider is still skipped here. This is
			// the real state of every approved Ozav customer measured on
			// 03/09 (e.g. missing_fields: [monthly_income_usd, is_pep]);
			// reading the full pending-step list instead would re-send their
			// identity documents on every retry meant for another provider.
			"approved owing only risk-profile fields",
			models.KycStatusApproved,
			[]string{"monthly_income_usd", "is_pep"},
			true,
		},
		{
			// A document step, unlike the profile fields above: this one a
			// document call can actually satisfy, so it is not skipped.
			"approved owing a proof of address",
			models.KycStatusApproved,
			[]string{"proof_of_address"},
			false,
		},
		{"approved owing a real step", models.KycStatusApproved, []string{"live_selfie"}, false},
		{"under review is analysing the set it has", models.KycStatusUnderReview, nil, true},
		// Caught by a live smoke: an application in flight owes nothing yet
		// only because it has not been told anything yet, and the documents
		// belong to the same pass of the wizard. Skipping here meant the user's
		// documents never reached that provider at all.
		{"an application in flight still receives the documents behind it", models.KycStatusSubmitting, nil, false},
		{"under review is skipped even owing a step", models.KycStatusUnderReview, []string{"live_selfie"}, true},
		{"paused must not be reopened", models.KycStatusPaused, []string{"live_selfie"}, true},
		{"incomplete owing a step receives", models.KycStatusIncomplete, []string{"government_id"}, false},
		{"requires info owing a step receives", models.KycStatusRequiresInfo, []string{"mother_name"}, false},
		{"rejected can be retried", models.KycStatusRejected, []string{"government_id"}, false},
		{
			// A rejected row falls through to the default case, same as
			// approved: a profile-only requirement is not this call's to fix.
			"rejected owing only a profile field is skipped",
			models.KycStatusRejected,
			[]string{"occupation"},
			true,
		},
		{
			"rejected owing a selfie receives",
			models.KycStatusRejected,
			[]string{"selfie"},
			false,
		},
		{"incomplete owing nothing actionable is skipped", models.KycStatusIncomplete, []string{"external_account"}, true},
		// A bank statement used to be the example of "nothing actionable":
		// the wizard had no step for it. It has one now, and it is a file.
		{"incomplete owing a bank statement receives", models.KycStatusIncomplete, []string{"bank_statement"}, false},
		// Measured on a real customer (03/09): with the risk-profile fields
		// filled, Ozav reports `missing_fields: []` and holds the record at
		// `pending` with `verification.checks` empty — it has verified nothing
		// because it has received no documents. Naming nothing is not the same
		// statement as naming something that does not gate: the first says the
		// provider cannot tell us what it wants, and the only move left is to
		// send what we have.
		//
		// Skipping here is what produced `ozav skipped in fan-out: nothing owed
		// at ozav (status incomplete)` in production while the user's documents
		// were dropped, leaving a customer that could never be verified.
		{"incomplete naming nothing at all still receives", models.KycStatusIncomplete, nil, false},
		{"requires info naming nothing at all still receives", models.KycStatusRequiresInfo, nil, false},
		// Without this exception no first submission would ever leave: on a
		// fresh row nobody owes anything yet.
		{"not started always receives", models.KycStatusNotStarted, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, kyc.ShouldSkipRedistribution(tt.status, tt.requirements))
		})
	}
}

// seedProviderStates makes the pre-loop reads answer with these rows, each
// provider holding a customer — the state the fan-out filters decide from.
// The integrations read is Maybe: StartTosForEnabled reads the rows alone.
func (h *fanOutHarness) seedProviderStates(rows ...models.CustomerProviderKyc) {
	customers := make([]string, 0, len(rows))
	for _, row := range rows {
		customers = append(customers, row.Provider)
	}
	h.seedSnapshot(rows, customers...)
}

// seedSnapshot is seedProviderStates with the customers named: a provider not
// among them holds none.
func (h *fanOutHarness) seedSnapshot(rows []models.CustomerProviderKyc, customers ...string) {
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return(rows, nil).Once()
	links := make([]models.CustomerProviderIntegration, 0, len(customers))
	for _, provider := range customers {
		link := linkedIntegration("cus_" + provider)
		link.Provider = provider
		links = append(links, *link)
	}
	h.integRepo.EXPECT().ListByCustomer(uint(7)).Return(links, nil).Maybe()
}

func providerState(provider, status string, requirements ...string) models.CustomerProviderKyc {
	row := models.CustomerProviderKyc{
		Provider: provider, KycStatus: status,
		Endorsements: "[]", Requirements: "[]", RejectionReasons: "[]", RawStatus: "{}",
	}
	row.SetRequirements(requirements)
	return row
}

// The regression the filter exists to prevent: a resubmission for one
// provider's missing selfie also re-sending documents to a provider that had
// already approved, which can take that customer back to under_review and cost
// the user a rail they already had.
func TestSubmitDocumentsToEnabledSkipsProvidersThatOweNothing(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.seedProviderStates(
		providerState(models.ProviderBridge, models.KycStatusIncomplete, "live_selfie"),
		// Approved, and carrying risk-profile fields the way an approved Ozav
		// customer always does. A filter reading raw requirements would never
		// skip this one; DocumentSteps is what still does, since a document
		// call cannot satisfy a profile field.
		providerState(models.ProviderOzav, models.KycStatusApproved, "is_pep", "occupation"),
	)
	h.expectAcceptedDocumentsSubmit(models.ProviderBridge, "stg_bridge")

	outcomes, err := h.svc.SubmitDocumentsToEnabled(context.Background(), h.user, 7, fullDocuments())
	require.NoError(t, err)
	require.Len(t, outcomes, 2)

	require.Equal(t, models.ProviderBridge, outcomes[0].Provider)
	require.True(t, outcomes[0].Accepted())
	require.Equal(t, models.ProviderOzav, outcomes[1].Provider)
	require.True(t, outcomes[1].Skipped)
	require.False(t, outcomes[1].Accepted(), "a skipped provider took nothing")
	require.NoError(t, outcomes[1].Err, "and refused nothing")

	h.staging.AssertNumberOfCalls(t, "Put", 1)
	h.dispatcher.AssertNumberOfCalls(t, "Dispatch", 1)
}

// A resubmission nobody needed is a no-op, not a failure. Reporting it as one
// would put an error on the user's screen for a request where nothing was
// wrong.
func TestSubmitDocumentsToEnabledSucceedsWhenEveryProviderIsSkipped(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.seedProviderStates(
		providerState(models.ProviderBridge, models.KycStatusApproved),
		providerState(models.ProviderOzav, models.KycStatusApproved, "is_pep"),
	)

	outcomes, err := h.svc.SubmitDocumentsToEnabled(context.Background(), h.user, 7, fullDocuments())
	require.NoError(t, err, "nothing to do is not an error")
	require.Len(t, outcomes, 2, "a skipped provider still has to appear, or the caller cannot tell it from one that was never configured")
	for _, outcome := range outcomes {
		require.True(t, outcome.Skipped)
	}

	h.staging.AssertNumberOfCalls(t, "Put", 0)
	h.dispatcher.AssertNumberOfCalls(t, "Dispatch", 0)
}

// Without the not_started exception the very first submission would reach
// nobody: on a fresh row there is nothing outstanding yet.
func TestSubmitDocumentsToEnabledNeverSkipsAFirstSubmission(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.seedProviderStates(
		providerState(models.ProviderBridge, models.KycStatusNotStarted),
		providerState(models.ProviderOzav, models.KycStatusNotStarted),
	)
	h.expectAcceptedDocumentsSubmit(models.ProviderBridge, "stg_bridge")
	h.expectAcceptedDocumentsSubmit(models.ProviderOzav, "stg_ozav")

	outcomes, err := h.svc.SubmitDocumentsToEnabled(context.Background(), h.user, 7, fullDocuments())
	require.NoError(t, err)
	require.Len(t, outcomes, 2)
	for _, outcome := range outcomes {
		require.True(t, outcome.Accepted())
	}
}

// What replaced the client's sixty-second poll. The application job creates the
// customer; the documents job, queued moments later, may find no customer yet.
//
// It hands the work back to the queue instead of waiting for one. Waiting held
// the only worker — `concurrent: 1` — for the whole worker timeout, and if this
// job was dequeued ahead of the application that creates the customer, it was
// waiting on a job queued behind it: a stall no amount of waiting could resolve,
// ending with the documents dropped.
//
// The payload stays staged, which is what keeps the cost at one upload: a
// client that waited instead would re-upload and re-stage on every attempt.
func TestProcessStagedDocumentsYieldsTheWorkerUntilTheCustomerExists(t *testing.T) {
	h := newHarnessWithQueue(t, "database")

	docs := stagedDocuments()
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindDocuments,
		Documents: &docs, PreviousStatus: models.KycStatusSubmitting,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	// The row still says `submitting`: the application ahead is in flight, so
	// there is someone to wait for.
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusSubmitting), nil).Once()
	h.dispatcher.EXPECT().
		DispatchAfter("stg_1", uint(7), models.ProviderBridge, testifymock.Anything).
		Return(nil).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))

	// The payload must survive: it is the whole submission, and the next
	// attempt has nothing to send without it.
	h.staging.AssertNotCalled(t, "Forget", "stg_1")
	h.adapter.AssertNotCalled(t, "SubmitDocuments",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// A re-queue that cannot be queued is the end of the line: the payload goes and
// the row is released, because nothing else clears `submitting` and the app is
// waiting on the event stream for an outcome.
func TestProcessStagedDocumentsReleasesTheRowWhenItCannotRequeue(t *testing.T) {
	h := newHarnessWithQueue(t, "database")

	docs := stagedDocuments()
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindDocuments,
		Documents: &docs, PreviousStatus: models.KycStatusSubmitting,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.dispatcher.EXPECT().
		DispatchAfter("stg_1", uint(7), models.ProviderBridge, testifymock.Anything).
		Return(errors.New("queue down")).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusSubmitting), nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
}

// An application never waits: the customer is what it creates.
func TestProcessStagedApplicationDoesNotWaitForACustomer(t *testing.T) {
	h := newHarness(t)

	app := stagedApplication()
	integ := linkedIntegration("")
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindApplication,
		Application: &app, PreviousStatus: models.KycStatusNotStarted,
	}, nil).Once()
	// Exactly one read. A second would mean the application was waiting for
	// something only it can produce.
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(integ, nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusSubmitting), nil).Once()
	h.adapter.EXPECT().SubmitApplication(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{ExternalCustomerID: "cus_1", Status: models.KycStatusUnderReview}, nil).Once()
	h.kycRepo.EXPECT().SaveWithLink(testifymock.Anything, false, integ, testifymock.Anything).Return(true, nil).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
	h.integRepo.AssertNumberOfCalls(t, "FindByCustomerAndProvider", 1)
}

// The agreement id is the provider's word relayed by the client. Nothing about
// it can be checked here — but that a session was opened at all can be, and
// without that check a stale id, a truncated postMessage or a typo is written
// down as a signature. The row then names an agreement the provider will refuse
// on the customer it is presented with.
func TestRecordTosAcceptancesRefusesAnIdForTermsNobodyOpened(t *testing.T) {
	h := newHarness(t)

	h.adapter.EXPECT().RequiresTos().Return(true).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusNotStarted), nil).Once()

	_, err := h.svc.RecordTosAcceptances(context.Background(), h.user, 7, []kyc.TosAcceptance{
		{Provider: models.ProviderBridge, SignedAgreementID: "sa_from_nowhere"},
	})
	require.ErrorIs(t, err, ramp.ErrTosSessionUnknown)
	h.kycRepo.AssertNotCalled(t, "Update", testifymock.Anything)
}

// A batch is one answer. Applying half of it under an error the client is told
// to retry leaves the two sides disagreeing about which providers are signed —
// and the signed half is then skipped when the client asks what is left, so the
// retry cannot even show the user the step they are stuck on.
func TestRecordTosAcceptancesWritesNothingWhenAnyEntryIsInvalid(t *testing.T) {
	h := newHarness(t)

	// The check runs over every entry before the first write.
	h.adapter.EXPECT().RequiresTos().Return(true).Once()
	h.adapter.EXPECT().RequiresTos().Return(false).Once()

	_, err := h.svc.RecordTosAcceptances(context.Background(), h.user, 7, []kyc.TosAcceptance{
		{Provider: models.ProviderBridge, SignedAgreementID: "sa_1"},
		{Provider: models.ProviderBridge, SignedAgreementID: "sa_2"},
	})
	require.ErrorIs(t, err, ramp.ErrTosNotRequired)
	h.kycRepo.AssertNotCalled(t, "FindByCustomerAndProvider", uint(7), models.ProviderBridge)
	h.kycRepo.AssertNotCalled(t, "Update", testifymock.Anything)
}

// Asked of the row, before the provider is asked for a page. Reading the
// signature off what startTos returns meant a signed provider still paid for a
// real session create, a fresh URL written over the stored one and a lock — all
// for a value discarded on the next line.
func TestStartTosForEnabledDoesNotReopenSignedTerms(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)

	signed := *kycRow(models.KycStatusNotStarted)
	signed.TosStatus = models.TosStatusApproved
	// One read answers every provider, and it is the only one: a signed
	// provider never reaches startTos, so no second read happens either.
	h.seedProviderStates(signed)

	sessions, err := h.svc.StartTosForEnabled(context.Background(), 7, dto.StartTosDTO{Modes: []string{kyc.TosModeHosted, kyc.TosModeAttested}})
	require.NoError(t, err)
	require.Empty(t, sessions)
	h.adapters[models.ProviderBridge].AssertNotCalled(t, "CreateTosSession",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// An empty list is a valid answer — most stacks have no terms — but it cannot
// also be how a total outage reads. The client skips the terms step on an empty
// list and then fails on tos_required two steps later, telling the user nothing
// about what actually broke.
func TestStartTosForEnabledReportsAnOutageRatherThanAnEmptyList(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	adapter := h.adapters[models.ProviderBridge]

	// Two different questions, one read each: the fan-out lists every row to
	// see which terms are already signed, and startTos then asks this one row
	// whether it holds a session worth reusing.
	h.seedProviderStates(*kycRow(models.KycStatusNotStarted))
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusNotStarted), nil).Once()
	adapter.EXPECT().RequiresTos().Return(true).Once()
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	adapter.EXPECT().CreateTosSession(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, errors.New("bridge 502")).Once()

	_, err := h.svc.StartTosForEnabled(context.Background(), 7, dto.StartTosDTO{Modes: []string{kyc.TosModeHosted, kyc.TosModeAttested}})
	require.Error(t, err, "nobody has terms and everybody failed must not read alike")
}

// Documents queued behind an application capture `submitting` as their previous
// status, and restoreStatus maps that to `incomplete`. So a documents failure
// arriving after the application worker had already moved the row on would
// write `incomplete` over `under_review` — regressing the user's compliance
// state and closing rails their endorsements had opened. Before documents were
// allowed to chain, the case could not arise.
func TestProcessStagedDocumentsFailureDoesNotRegressAnAdvancedRow(t *testing.T) {
	h := newHarness(t)

	docs := stagedDocuments()
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindDocuments,
		Documents: &docs, PreviousStatus: models.KycStatusSubmitting,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	// The application worker got there first and moved the row on.
	advanced := kycRow(models.KycStatusUnderReview)
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(advanced, nil).Once()
	h.adapter.EXPECT().SubmitDocuments(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, errors.New("bridge 503")).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	// The failure is recorded on the row it found, on the status it found.
	h.kycRepo.EXPECT().UpdateIfStatus(advanced, models.KycStatusUnderReview).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
	require.Equal(t, models.KycStatusUnderReview, advanced.KycStatus,
		"a failed documents send changed nothing upstream; it must not rewrite what did")
	require.NotNil(t, advanced.LastErrorCode)
}

// A submission whose upstream call succeeded but whose write failed used to
// leave the row on `submitting` forever: the only branch that restores a
// status is the one for a failed send, and nothing later clears `submitting`
// — no webhook, no refresh, no retry, because the client is told a submission
// is already in flight.
//
// Measured 01/09 against the live sandbox: an Ozav customer adopted after
// CUSTOMER_ALREADY_EXISTS collided with the (provider, external_customer_id)
// unique index, the write failed, and the user's KYC sat on `submitting`
// with no path out. The write can fail for reasons that have nothing to do
// with that case — a dropped connection, a deadlock — and the outcome must
// not be a permanently stuck account either way.
func TestProcessStagedSubmissionReleasesTheRowWhenTheWriteFails(t *testing.T) {
	h := newHarness(t)

	app := stagedApplication()
	integ := linkedIntegration("")
	row := kycRow(models.KycStatusSubmitting)
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindApplication,
		Application: &app, PreviousStatus: models.KycStatusIncomplete,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(integ, nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	h.adapter.EXPECT().SubmitApplication(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{
			ExternalCustomerID: "cus_taken",
			Status:             models.KycStatusUnderReview,
			Raw:                []byte(`{"id":"cus_taken"}`),
		}, nil).Once()
	h.staging.EXPECT().Forget("stg_1").Once()

	// The link write is what fails, the way the unique index makes it fail.
	h.kycRepo.EXPECT().SaveWithLink(row, false, integ, testifymock.Anything).
		Return(false, errors.New("duplicate key value violates unique constraint")).Once()
	// The release is a second, plain write of the row alone.
	h.kycRepo.EXPECT().UpdateIfStatus(row, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.KycStatus == models.KycStatusIncomplete && e.FailureReason != ""
	})).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
	require.Equal(t, models.KycStatusIncomplete, row.KycStatus,
		"a row the write could not advance must fall back to what the user can act on")
}

// The release must also undo what the provider's answer had already written
// into the row. Endorsements recorded for a customer the link row cannot name
// advertise an open rail that no call can reach: the router would clear the
// KYC gate and then fail on the missing customer, which reads to the user as
// "verified, but nothing works".
func TestProcessStagedSubmissionDiscardsComplianceStateItCouldNotPersist(t *testing.T) {
	h := newHarness(t)

	app := stagedApplication()
	integ := linkedIntegration("")
	row := kycRow(models.KycStatusSubmitting)
	row.Endorsements = "[]"
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindApplication,
		Application: &app, PreviousStatus: models.KycStatusIncomplete,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(integ, nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	h.adapter.EXPECT().SubmitApplication(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{
			ExternalCustomerID: "cus_taken",
			Status:             models.KycStatusApproved,
			Endorsements: []models.Endorsement{
				{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved},
			},
			Raw: []byte(`{"id":"cus_taken"}`),
		}, nil).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	h.kycRepo.EXPECT().SaveWithLink(row, false, integ, testifymock.Anything).
		Return(false, errors.New("duplicate key value violates unique constraint")).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(row, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
	require.Equal(t, "[]", row.Endorsements,
		"an endorsement nothing can act on must not be recorded")
	require.Equal(t, models.KycStatusIncomplete, row.KycStatus)
}

// --- submit: what the row remembers about a failure --------------------------

// A refusal the worker meets is the only outcome the request never sees: it
// answered 202 long before. The row is where the user and the admin find it
// afterwards, in the same vocabulary as the HTTP codes, so a provider that
// wants terms signed does not read as a provider that is down.
func TestProcessStagedSubmissionRecordsWhyItFailed(t *testing.T) {
	h := newHarness(t)

	app := stagedApplication()
	persisted := kycRow(models.KycStatusSubmitting)
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindApplication,
		Application: &app, PreviousStatus: models.KycStatusIncomplete,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(persisted, nil).Once()
	h.adapter.EXPECT().SubmitApplication(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, fmt.Errorf("ozav: POST /customers: upstream rejected (TERMS_ACCEPTANCE_REQUIRED): %w",
			ramp.ErrTosRequired)).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(persisted, testifymock.Anything).Return(true, nil).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.FailureReason != "" && e.FailureCode == "tos_required"
	})).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))

	require.NotNil(t, persisted.LastErrorCode)
	require.Equal(t, "tos_required", *persisted.LastErrorCode)
	require.NotNil(t, persisted.LastErrorDetail)
	require.Contains(t, *persisted.LastErrorDetail, "TERMS_ACCEPTANCE_REQUIRED",
		"the upstream text is what the admin quotes to the provider")
	require.NotNil(t, persisted.LastErrorAt)
}

// A failure nobody can name still gets recorded, under the generic code —
// the same one the HTTP layer would have answered had this been synchronous.
func TestProcessStagedSubmissionRecordsTheDefaultCodeForAnUnknownFailure(t *testing.T) {
	h := newHarness(t)

	app := stagedApplication()
	persisted := kycRow(models.KycStatusSubmitting)
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindApplication,
		Application: &app, PreviousStatus: models.KycStatusIncomplete,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(persisted, nil).Once()
	h.adapter.EXPECT().SubmitApplication(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, errors.New("dial tcp: connection refused")).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(persisted, testifymock.Anything).Return(true, nil).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.FailureCode == ramp.DefaultErrorCode
	})).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
	require.NotNil(t, persisted.LastErrorCode)
	require.Equal(t, ramp.DefaultErrorCode, *persisted.LastErrorCode)
}

// The provider accepting a submission is what makes an old refusal stale. Left
// on the row, "terms not signed" would sit next to "under review" and the app
// would keep showing a problem that no longer exists.
func TestProcessStagedSubmissionClearsTheLastErrorOnSuccess(t *testing.T) {
	h := newHarness(t)

	app := stagedApplication()
	integ := linkedIntegration("")
	persisted := kycRow(models.KycStatusSubmitting)
	persisted.RecordSubmissionFailure("tos_required", "earlier refusal")
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindApplication,
		Application: &app, PreviousStatus: models.KycStatusIncomplete,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(integ, nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(persisted, nil).Once()
	h.adapter.EXPECT().SubmitApplication(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{ExternalCustomerID: "cus_1", Status: models.KycStatusUnderReview}, nil).Once()
	h.kycRepo.EXPECT().SaveWithLink(testifymock.Anything, false, integ, testifymock.Anything).Return(true, nil).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.FailureCode == "" && e.FailureReason == ""
	})).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
	require.Nil(t, persisted.LastErrorCode)
	require.Nil(t, persisted.LastErrorDetail)
	require.Nil(t, persisted.LastErrorAt)
}

// Documents wait for a customer only while an application is in flight to
// create one. In production the application was refused, the wizard sent its
// documents a second later, and the worker re-queued them every second for the
// fifteen minutes it took the payload to expire — the user watching
// "submitting" the whole time, the refusal already recorded and unread.
//
// The documents captured `incomplete` as their previous status, which is the
// tell: an application ahead of them would have left the row on `submitting`.
func TestProcessStagedDocumentsGiveUpWhenNoApplicationIsInFlight(t *testing.T) {
	h := newHarnessWithQueue(t, "database")

	docs := stagedDocuments()
	row := kycRow(models.KycStatusSubmitting)
	row.RecordSubmissionFailure("tos_required", "the application's refusal")
	h.staging.EXPECT().Get("stg_docs").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindDocuments,
		Documents: &docs, PreviousStatus: models.KycStatusIncomplete,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil)
	h.staging.EXPECT().Forget("stg_docs").Once()
	h.kycRepo.EXPECT().UpdateIfStatus(row, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.KycStatus == models.KycStatusIncomplete && e.FailureCode == "tos_required"
	})).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_docs", 7, models.ProviderBridge))

	h.dispatcher.AssertNotCalled(t, "DispatchAfter",
		testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
	require.Equal(t, models.KycStatusIncomplete, row.KycStatus)
	require.NotNil(t, row.LastErrorCode)
	require.Equal(t, "tos_required", *row.LastErrorCode,
		"the application's refusal is the answer; the documents add nothing to it")
}

// The other order: documents queued behind an application that then failed.
// They captured `submitting`, but the application's release has since moved
// the row off it — so nothing is in flight, and waiting is waiting for nobody.
func TestProcessStagedDocumentsGiveUpWhenTheApplicationAlreadyFailed(t *testing.T) {
	h := newHarnessWithQueue(t, "database")

	docs := stagedDocuments()
	row := kycRow(models.KycStatusIncomplete)
	h.staging.EXPECT().Get("stg_docs").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindDocuments,
		Documents: &docs, PreviousStatus: models.KycStatusSubmitting,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil)
	h.staging.EXPECT().Forget("stg_docs").Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_docs", 7, models.ProviderBridge))

	h.dispatcher.AssertNotCalled(t, "DispatchAfter",
		testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
	// The row was already released by the application; there is nothing to
	// write and nothing new to announce.
	h.kycRepo.AssertNotCalled(t, "Update", testifymock.Anything)
}

// An expired payload is a failure with a name of its own: nothing was refused
// upstream, the images simply outlived the worker's chance to send them.
func TestProcessStagedSubmissionRecordsAnExpiredPayload(t *testing.T) {
	h := newHarness(t)

	row := kycRow(models.KycStatusSubmitting)
	h.staging.EXPECT().Get("stg_gone").Return(nil, kyc.ErrStagingExpired).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(row, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.FailureCode == kyc.FailureCodeSubmissionExpired
	})).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_gone", 7, models.ProviderBridge))
	require.NotNil(t, row.LastErrorCode)
	require.Equal(t, kyc.FailureCodeSubmissionExpired, *row.LastErrorCode)
}

// --- terms sessions a provider issues before they are accepted --------------

// The token has to survive the call that created it: Ozav returns it once and
// keeps only its hash, so a row that does not persist it here can never name it
// on a customer. What must NOT happen is the row reading as signed — the
// submission gate goes by the agreement id, and writing one now would let an
// unaccepted session through it.
func TestStartTosPersistsTheSessionTokenWithoutApprovingTheTerms(t *testing.T) {
	h := newHarness(t)
	row := kycRow(models.KycStatusNotStarted)
	expires := time.Now().Add(24 * time.Hour)

	h.adapter.EXPECT().CreateTosSession(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.TosSession{
			URL: "https://ozav.io/terms", SessionID: "ts_8Kd2mQ4v", ExpiresAt: expires,
		}, nil).Once()
	expectStartTosMocks(h, row)

	saved, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")

	require.NoError(t, err)
	require.NotNil(t, saved.TosSessionID)
	require.Equal(t, "ts_8Kd2mQ4v", *saved.TosSessionID)
	require.NotNil(t, saved.TosExpiresAt)
	require.Equal(t, expires.Unix(), saved.TosExpiresAt.Unix())
	require.Equal(t, models.TosStatusPending, saved.TosStatus)
	require.Nil(t, saved.SignedAgreementID, "nothing has been accepted yet")
}

// Asking again is not free the way a repeated GET is: a provider that issues
// the token once answers the second request with a session carrying no token
// at all, and that session can never be provisioned against. The row is the
// deduplication, so a live session there means no upstream call happens —
// which the mock enforces by failing on any it does not expect.
func TestStartTosReusesALiveSessionInsteadOfMintingAnother(t *testing.T) {
	h := newHarness(t)
	row := kycRow(models.KycStatusNotStarted)
	id, url := "ts_alive", "https://ozav.io/terms"
	expires := time.Now().Add(24 * time.Hour)
	row.TosSessionID, row.TosLinkURL, row.TosExpiresAt = &id, &url, &expires

	h.adapter.EXPECT().RequiresTos().Return(true).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()

	saved, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")

	require.NoError(t, err)
	require.Equal(t, "ts_alive", *saved.TosSessionID)
}

// A session that has died is worse than no session: presenting it sends the
// user through the whole terms step to have the customer create refused at the
// end. Once it is past its deadline the row stops standing in for one.
func TestStartTosMintsAFreshSessionWhenTheStoredOneHasExpired(t *testing.T) {
	h := newHarness(t)
	row := kycRow(models.KycStatusNotStarted)
	id, url := "ts_dead", "https://ozav.io/terms"
	expired := time.Now().Add(-time.Hour)
	row.TosSessionID, row.TosLinkURL, row.TosExpiresAt = &id, &url, &expired

	h.adapter.EXPECT().CreateTosSession(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.TosSession{
			URL: url, SessionID: "ts_fresh", ExpiresAt: time.Now().Add(24 * time.Hour),
		}, nil).Once()
	expectStartTosMocks(h, row)

	saved, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")

	require.NoError(t, err)
	require.Equal(t, "ts_fresh", *saved.TosSessionID)
}

// --- recording an acceptance XIP collected itself ---------------------------

// attestingAdapter is a ProviderAdapter that also attests terms, which is how
// the service tells the two shapes apart: a plain ProviderAdapter hosts its own
// terms page, one that satisfies ramp.TosAttester does not.
type attestingAdapter struct {
	*mocks.MockProviderAdapter
	*mocks.MockTosAttester
}

func (a attestingAdapter) Code() ramp.ProviderCode { return a.MockProviderAdapter.Code() }

type recorderFunc func(rampsvc.Entry)

func (f recorderFunc) Record(e rampsvc.Entry) { f(e) }

func newAttestingHarness(t *testing.T) (*harness, *mocks.MockTosAttester) {
	t.Helper()
	h := newHarness(t)
	attester := mocks.NewMockTosAttester(t)
	h.svc = kyc.NewProviderKycService(kyc.Deps{
		Factory:  ramp.NewFactory(attestingAdapter{h.adapter, attester}),
		Config:   ramp.NewConfig(mocks.NewMockSettingsSource(t)),
		Linker:   h.linker,
		Kyc:      h.kycRepo,
		Profiles: h.profileRepo,
		Persons:  h.persons, Members: h.members,
		Integrations: h.integRepo,
		Staging:      h.staging,
		Dispatcher:   h.dispatcher,
		Locker:       passthroughLocker{},
		Broadcaster:  h.broadcaster,
		Notifier:     h.notifier,
	})
	return h, attester
}

func rowWithLiveTosSession() *models.CustomerProviderKyc {
	row := kycRow(models.KycStatusNotStarted)
	id, url := "ts_live", "https://ozav.io/terms"
	expires := time.Now().Add(24 * time.Hour)
	row.TosSessionID, row.TosLinkURL, row.TosExpiresAt = &id, &url, &expires
	return row
}

// The whole point of an attesting provider: it never saw the user accept, so
// this side has to say so upstream before the row may claim it. Only after that
// call succeeds does the session token become the agreement id — the value the
// submission gate reads as proof the terms were signed.
func TestRecordTosAcceptancesAttestsUpstreamBeforeMarkingTheRowSigned(t *testing.T) {
	h, attester := newAttestingHarness(t)
	row := rowWithLiveTosSession()

	h.adapter.EXPECT().RequiresTos().Return(true).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Twice()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, row.KycStatus).Return(true, nil).Once()
	attester.EXPECT().AcceptTos(testifymock.Anything, testifymock.Anything,
		testifymock.MatchedBy(func(a ramp.TosAttestation) bool {
			return a.SessionID == "ts_live" && a.LegalName == "Marina Alves Ribeiro" &&
				a.Email == "luiza@example.com" && a.IPAddress == "203.0.113.42" &&
				a.UserAgent == "xip-android/1.0.3"
		})).Return(nil).Once()

	updated, err := h.svc.RecordTosAcceptances(context.Background(), h.user, 7, []kyc.TosAcceptance{{
		Provider: models.ProviderBridge, LegalName: "Marina Alves Ribeiro",
		IPAddress: "203.0.113.42", UserAgent: "xip-android/1.0.3",
	}})

	require.NoError(t, err)
	require.Len(t, updated, 1)
	require.Equal(t, models.TosStatusApproved, updated[0].TosStatus)
	require.Equal(t, "ts_live", *updated[0].SignedAgreementID)
	require.NotNil(t, updated[0].TosAcceptedAt)
}

// The name is the whole of the attestation: the provider takes our word for who
// accepted, and an empty one attests on behalf of nobody. Refused before the
// call, so nothing upstream records an acceptance we cannot stand behind.
func TestRecordTosAcceptancesRefusesAnAttestationWithNoName(t *testing.T) {
	h, _ := newAttestingHarness(t)

	h.adapter.EXPECT().RequiresTos().Return(true).Once()
	// No FindByID: the missing name is a local check, caught before the
	// batch would ever look up who is doing the attesting, let alone call
	// the provider on their behalf.
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(rowWithLiveTosSession(), nil).Once()

	_, err := h.svc.RecordTosAcceptances(context.Background(), h.user, 7,
		[]kyc.TosAcceptance{{Provider: models.ProviderBridge}})

	require.ErrorIs(t, err, ramp.ErrTosLegalNameRequired)
}

// For an attesting provider the proof that a session was opened is the token,
// not the URL: its terms page is a static address that says nothing about this
// user. Checking the URL would accept an attestation for a session that was
// never minted, and the customer create would then be refused.
func TestRecordTosAcceptancesRefusesAnAttestationWithNoSession(t *testing.T) {
	h, _ := newAttestingHarness(t)
	row := kycRow(models.KycStatusNotStarted)
	url := "https://ozav.io/terms"
	row.TosLinkURL = &url

	h.adapter.EXPECT().RequiresTos().Return(true).Once()
	// No FindByID: no session to attest against is also a local check, and
	// it fails the batch before any user lookup or provider call.
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()

	_, err := h.svc.RecordTosAcceptances(context.Background(), h.user, 7, []kyc.TosAcceptance{{
		Provider: models.ProviderBridge, LegalName: "Marina Alves Ribeiro",
	}})

	require.ErrorIs(t, err, ramp.ErrTosSessionUnknown)
}

// A session the provider has stopped honouring must not stay on the row: the
// next terms step reads it as live and presents it again, and every attempt
// after that fails the same way. Forgetting it is what lets the step recover.
func TestRecordTosAcceptancesForgetsASessionTheProviderRefused(t *testing.T) {
	h, attester := newAttestingHarness(t)
	row := rowWithLiveTosSession()

	h.adapter.EXPECT().RequiresTos().Return(true).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Twice()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, row.KycStatus).Return(true, nil).Once()
	attester.EXPECT().AcceptTos(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(fmt.Errorf("ozav: accept: %w", ramp.ErrTosSessionExpired)).Once()

	_, err := h.svc.RecordTosAcceptances(context.Background(), h.user, 7, []kyc.TosAcceptance{{
		Provider: models.ProviderBridge, LegalName: "Marina Alves Ribeiro",
	}})

	require.ErrorIs(t, err, ramp.ErrTosSessionExpired)
	require.Nil(t, row.TosSessionID, "the dead session must not survive on the row")
	require.Equal(t, models.TosStatusPending, row.TosStatus)
}

// The submission runs on a worker, so a refusal on the terms arrives long after
// the request that recorded the acceptance answered. Left as it was, the row
// stays `approved` holding an agreement id the provider no longer honours: the
// terms step skips it from then on — it only offers providers that are not
// approved — while every resubmission re-sends the same dead id and is refused
// identically. Nothing in the flow breaks that loop, so the release does.
func TestProcessStagedApplicationForgetsTermsTheProviderRefused(t *testing.T) {
	h := newHarness(t)

	signed := kycRow(models.KycStatusSubmitting)
	id, agreement := "ts_dead", "ts_dead"
	signed.TosStatus, signed.TosSessionID, signed.SignedAgreementID = models.TosStatusApproved, &id, &agreement

	app := stagedApplication()
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindApplication,
		Application: &app, PreviousStatus: models.KycStatusIncomplete,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(signed, nil).Once()
	h.adapter.EXPECT().SubmitApplication(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, fmt.Errorf("ozav: create customer: %w", ramp.ErrTosRequired)).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))

	require.Equal(t, models.TosStatusPending, signed.TosStatus, "the terms step has to run again")
	require.Nil(t, signed.SignedAgreementID, "the agreement built from a refused session is worthless")
	require.Nil(t, signed.TosSessionID)
	require.Equal(t, "tos_required", *signed.LastErrorCode)
}

// A failure that has nothing to do with the terms must leave them alone: the
// signature is still good, and throwing it away would send the user back
// through a step they completed for a provider that was merely unreachable.
func TestProcessStagedApplicationKeepsTermsWhenTheFailureIsUnrelated(t *testing.T) {
	h := newHarness(t)

	signed := kycRow(models.KycStatusSubmitting)
	id, agreement := "ts_live", "ts_live"
	signed.TosStatus, signed.TosSessionID, signed.SignedAgreementID = models.TosStatusApproved, &id, &agreement

	app := stagedApplication()
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindApplication,
		Application: &app, PreviousStatus: models.KycStatusIncomplete,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_1"), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(signed, nil).Once()
	h.adapter.EXPECT().SubmitApplication(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, errors.New("bridge 503")).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))

	require.Equal(t, models.TosStatusApproved, signed.TosStatus)
	require.Equal(t, "ts_live", *signed.SignedAgreementID)
}

// The app already in the Play Store draws every session as a button that opens
// the provider's page in a WebView and waits for it to post an agreement back.
// An attesting provider's page posts nothing, so such a row would leave that
// build stuck on the terms step forever — and the step gates the whole wizard,
// so it would take Bridge's KYC down with it for users who never touched Ozav.
//
// So the client says what it can render, and a client that says nothing is one
// that predates the question: hosted only.
func TestStartTosForEnabledHidesAttestedTermsFromAClientThatCannotRenderThem(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	adapter := h.adapters[models.ProviderBridge]
	attester := mocks.NewMockTosAttester(t)
	h.svc = kyc.NewProviderKycService(kyc.Deps{
		Factory:  ramp.NewFactory(attestingAdapter{adapter, attester}),
		Config:   h.config,
		Linker:   h.linker,
		Kyc:      h.kycRepo,
		Profiles: h.profileRepo,
		Persons:  h.persons, Members: h.members,
		Integrations: h.integRepo,
		Staging:      h.staging,
		Dispatcher:   h.dispatcher,
		Locker:       passthroughLocker{},
		Broadcaster:  h.broadcaster,
		Notifier:     h.notifier,
	})

	h.seedProviderStates(*kycRow(models.KycStatusNotStarted))
	// RequiresTos is never reached: the mode filter runs before startTos, so a
	// provider the client cannot render costs no adapter call at all.

	sessions, err := h.svc.StartTosForEnabled(context.Background(), 7, dto.StartTosDTO{})

	require.NoError(t, err)
	require.Empty(t, sessions, "an old client must be told nothing rather than handed a page it cannot finish")
}

// The night this whole table was built for. A user's documents reach this
// backend, the fan-out decides the provider owes nothing, and nothing is sent
// — leaving an operator and a user both believing the documents were
// delivered, with the only trace a log line on a release directory since
// pruned.
//
// A call log shows calls. This asserts the *skip* is a line too, because a
// provider that was never asked is indistinguishable from one that was never
// attempted, and telling those apart is the entire point of the timeline.
func TestDocumentsFanOutRecordsTheProviderItSkipped(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.seedProviderStates(
		providerState(models.ProviderBridge, models.KycStatusApproved),
		providerState(models.ProviderOzav, models.KycStatusApproved, "is_pep"),
	)

	var seen []rampsvc.Entry
	h.svc = kyc.NewProviderKycService(kyc.Deps{
		Factory:  ramp.NewFactory(h.registered...),
		Config:   h.config,
		Linker:   h.linker,
		Kyc:      h.kycRepo,
		Profiles: h.profileRepo,
		Persons:  h.persons, Members: h.members,
		Integrations: h.integRepo,
		Staging:      h.staging,
		Dispatcher:   h.dispatcher,
		Locker:       passthroughLocker{},
		Broadcaster:  h.broadcaster,
		Notifier:     h.notifier,
		Timeline:     recorderFunc(func(e rampsvc.Entry) { seen = append(seen, e) }),
	})

	_, err := h.svc.SubmitDocumentsToEnabled(context.Background(), h.user, 7, fullDocuments())
	require.NoError(t, err)

	require.Len(t, seen, 2, "both providers were passed over, and both have to say so")
	for _, entry := range seen {
		require.Equal(t, rampsvc.OpSubmitDocuments, entry.Operation)
		require.Equal(t, uint(7), entry.UserID)
		require.Equal(t, uint(7), entry.CustomerID, "the person's own customer")
		// The exact token, not merely a non-empty one: it is persisted to
		// provider_calls and read back by the admin timeline, so a rename
		// would silently orphan every row written before it.
		require.Equal(t, "nothing_owed", entry.SkipReason,
			"a provider that was never asked leaves no call; the reason is what makes the absence readable")
	}
}

// The steps explain a refusal the user already has: a failed read costs the
// steps, never the 409 they belong to.
func TestPendingStepsReadsTheStoredRow(t *testing.T) {
	owing := func() *models.CustomerProviderKyc {
		row := kycRow(models.KycStatusIncomplete)
		row.Requirements, row.RejectionReasons = `["proof_of_address"]`, `["live_selfie"]`
		return row
	}
	application := []string{"personal", "profile", "address", "document", "selfie"}
	cases := map[string]struct {
		row  *models.CustomerProviderKyc
		err  error
		link *models.CustomerProviderIntegration
		want []string
	}{
		"owes steps": {row: owing(), link: linkedIntegration("cus_1"), want: []string{"proof_of_address", "selfie"}},
		// No customer anywhere upstream: whatever it named, it owes the whole
		// application first.
		"owes steps, no customer": {row: owing(), want: []string{"personal", "profile", "address", "proof_of_address", "document", "selfie"}},
		// A provider enabled after the user's first KYC.
		"never started": {want: application},
		"read fails":    {err: errors.New("pq: connection reset")},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(c.row, c.err).Once()
			if c.err == nil {
				h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(c.link, nil).Once()
			}
			if c.row == nil && c.err == nil {
				h.adapter.EXPECT().PreferredKycMode().Return(ramp.KycModeAPIDocs).Maybe()
				h.adapter.EXPECT().RequiresTos().Return(true).Maybe()
			}

			require.Equal(t, c.want, h.svc.PendingSteps(7, models.ProviderBridge))
		})
	}
}

func TestListReturnsTheCallersRowsForEnabledProviders(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	rows := []models.CustomerProviderKyc{*kycRow(models.KycStatusApproved)}
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return(rows, nil).Once()
	h.integRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderIntegration{
		*linkedIntegration("cus_1"),
	}, nil).Once()

	got, err := h.svc.List(7)
	require.NoError(t, err)
	require.Equal(t, rows, got)
}

// The provider is the one that decides the link exists: its refusal keeps its
// sentinel for the controller and leaves nothing written or announced.
func TestHostedLinkWritesNothingTheProviderRefused(t *testing.T) {
	h := newHarness(t)
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusNotStarted), nil).Once()
	h.adapter.EXPECT().CreateHostedKycLink(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, ramp.ErrHostedLinkUnavailable).Once()

	_, err := h.svc.HostedLink(context.Background(), h.user, 7, models.ProviderBridge, dto.HostedLinkDTO{FullName: "Luiza Souza"})
	require.ErrorIs(t, err, ramp.ErrHostedLinkUnavailable)
	h.kycRepo.AssertNotCalled(t, "SaveWithLink", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.broadcaster.AssertNotCalled(t, "PublishKyc", testifymock.Anything, testifymock.Anything)
}

// No profile is (nil, nil), which the controller answers 204 rather than 404.
func TestProfileAnswersTheRetainedFields(t *testing.T) {
	for name, retained := range map[string]*ramp.Profile{"retained": {FirstName: "Luiza"}, "never submitted": nil} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.retention.profile = retained

			got, err := h.svc.Profile(7)
			require.NoError(t, err)
			require.Equal(t, retained, got)
		})
	}
}
