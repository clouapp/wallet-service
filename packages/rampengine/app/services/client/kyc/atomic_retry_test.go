package kyc_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// retryStore is the stored KYC row and customer link of one person, written
// the way the repositories write them: a row only on the status it was
// decided from, and a link naming a customer only over no customer or that
// same one (repositories.ErrLinkConflict otherwise). It answers the harness's
// repository mocks, so a test can drive a whole retry across them.
type retryStore struct {
	mu   sync.Mutex
	row  models.CustomerProviderKyc
	link models.CustomerProviderIntegration
	// detached is every link DetachCustomer was handed, as handed.
	detached []models.CustomerProviderIntegration
}

func customerID(link *models.CustomerProviderIntegration) string {
	if link == nil || link.ExternalCustomerID == nil {
		return ""
	}
	return *link.ExternalCustomerID
}

func (s *retryStore) readRow(uint, string) (*models.CustomerProviderKyc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.row
	return &row, nil
}

func (s *retryStore) readLink(uint, string) (*models.CustomerProviderIntegration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	link := s.link
	return &link, nil
}

func (s *retryStore) start(context.Context, uint, string) (*models.CustomerProviderIntegration, error) {
	return s.readLink(0, "")
}

func (s *retryStore) updateIfStatus(row *models.CustomerProviderKyc, status string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.row.KycStatus != status {
		return false, nil
	}
	s.row = *row
	return true, nil
}

func (s *retryStore) saveWithLink(row *models.CustomerProviderKyc, _ bool, link *models.CustomerProviderIntegration, status string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if stored, next := customerID(&s.link), customerID(link); next != "" && stored != "" && stored != next {
		return false, repositories.ErrLinkConflict
	}
	s.link = *link
	if s.row.KycStatus != status {
		return false, nil
	}
	s.row = *row
	return true, nil
}

func (s *retryStore) detach(link *models.CustomerProviderIntegration, row, read *models.CustomerProviderKyc) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.detached = append(s.detached, *link)
	if customerID(&s.link) != customerID(link) {
		return false, repositories.ErrLinkConflict
	}
	if s.row.KycStatus != read.KycStatus || s.row.RejectionCategories != read.RejectionCategories {
		return false, nil
	}
	s.link.ExternalCustomerID = nil
	s.link.Status = models.IntegrationStatusPending
	s.row = *row
	*link = s.link
	return true, nil
}

// rejectedAtomicRow is a row the provider refused with a category the user
// can act on, holding everything the first attempt left: the accepted terms,
// their session and the application held for the documents.
func rejectedAtomicRow(categories ...string) *models.CustomerProviderKyc {
	row := kycRow(models.KycStatusRejected)
	agreement, session := "to_old", "tos_old"
	row.SignedAgreementID, row.TosSessionID = &agreement, &session
	row.TosStatus = models.TosStatusApproved
	deferred := time.Now().Add(-time.Hour)
	row.ApplicationDeferredAt = &deferred
	if len(categories) == 0 {
		categories = []string{models.KycRejectionDocumentUnreadable}
	}
	row.SetRejectionCategories(categories)
	row.SetRejectionReasons([]string{"the document could not be read"})
	return row
}

// wireRetryStore answers the harness's repositories and linker from store.
func wireRetryStore(h *harness, store *retryStore) {
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).RunAndReturn(store.readRow).Maybe()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).RunAndReturn(store.updateIfStatus).Maybe()
	h.kycRepo.EXPECT().SaveWithLink(testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(store.saveWithLink).Maybe()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).RunAndReturn(store.readLink).Maybe()
	h.integRepo.EXPECT().DetachCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(store.detach).Maybe()
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).RunAndReturn(store.start).Maybe()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Maybe()
	h.adapter.EXPECT().RequiresTos().Return(true).Maybe()
}

func newRetryStore(row *models.CustomerProviderKyc, customer string) *retryStore {
	return &retryStore{row: *row, link: *linkedIntegration(customer)}
}

// expectNewTosSession answers the one terms session the retry asks for, and
// only when it names no customer: the old one's terms are not the new one's.
func expectNewTosSession(h *harness) *string {
	var key string
	h.adapter.EXPECT().CreateTosSession(testifymock.Anything,
		testifymock.MatchedBy(func(ref ramp.CustomerRef) bool { return ref.ExternalID == "" }),
		testifymock.Anything).
		RunAndReturn(func(_ context.Context, _ ramp.CustomerRef, opts ramp.TosOpts) (*ramp.TosSession, error) {
			key = opts.IdempotencyKey
			return &ramp.TosSession{URL: "https://app.blindpay.com/e/terms-of-service", SessionID: "tos_new"}, nil
		}).Once()
	return &key
}

// A rejection the user can act on is retried with a new customer: the old one
// is detached, the terms and the verdict are cleared with the application held
// for it, and the new terms session is asked for without the old customer.
func TestAtomicRejectedStartTosDetachesTheCustomer(t *testing.T) {
	h, _ := newAtomicHarnessWith(t, "sync", &intentStore{journal: new([]string)})
	store := newRetryStore(rejectedAtomicRow(), "cu_old")
	wireRetryStore(h, store)
	key := expectNewTosSession(h)

	got, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")
	require.NoError(t, err)

	require.Len(t, store.detached, 1)
	require.Equal(t, "cu_old", customerID(&store.detached[0]), "the customer read is the one detached")
	require.Empty(t, customerID(&store.link))
	require.Equal(t, models.IntegrationStatusPending, store.link.Status)

	require.Equal(t, models.KycStatusNotStarted, got.KycStatus)
	require.Equal(t, models.TosStatusPending, got.TosStatus)
	require.Nil(t, got.SignedAgreementID, "the old agreement was the old customer's")
	require.NotNil(t, got.TosSessionID)
	require.Equal(t, "tos_new", *got.TosSessionID)
	require.Empty(t, got.DecodedRejectionCategories())
	require.Empty(t, got.DecodedRejectionReasons())
	require.Nil(t, got.ApplicationDeferredAt, "the next application is a new one")
	require.Equal(t, models.KycStatusNotStarted, store.row.KycStatus)
	require.NotEmpty(t, *key)
	require.NotRegexp(t, tosIdempotencyKeyPattern, *key,
		"a fresh key: the day's derived one already opened the old customer's terms")
}

// A terminal refusal is not retried: the provider never sees the user again.
func TestAtomicTerminalRejectionIsNotRetried(t *testing.T) {
	h, _ := newAtomicHarnessWith(t, "sync", &intentStore{journal: new([]string)})
	store := newRetryStore(rejectedAtomicRow(models.KycRejectionTaxIDInUse), "cu_old")
	wireRetryStore(h, store)

	_, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")
	require.ErrorIs(t, err, ramp.ErrKycRejected)
	require.Empty(t, store.detached)
	require.Equal(t, "cu_old", customerID(&store.link))
	h.adapter.AssertNotCalled(t, "CreateTosSession", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// A provider whose customer takes updates keeps it: nothing is detached, and
// the terms session is asked for the customer it has.
func TestNonAtomicRejectedStartTosKeepsTheCustomer(t *testing.T) {
	h := newHarness(t)
	store := newRetryStore(rejectedAtomicRow(), "cus_1")
	store.row.TosSessionID = nil
	wireRetryStore(h, store)
	h.adapter.EXPECT().CreateTosSession(testifymock.Anything,
		testifymock.MatchedBy(func(ref ramp.CustomerRef) bool { return ref.ExternalID == "cus_1" }),
		testifymock.Anything).
		Return(&ramp.TosSession{URL: "https://bridge.xyz/tos"}, nil).Once()

	got, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")
	require.NoError(t, err)
	require.Empty(t, store.detached)
	require.Equal(t, "cus_1", customerID(&store.link))
	require.Equal(t, models.KycStatusRejected, got.KycStatus)
	require.Equal(t, []string{models.KycRejectionDocumentUnreadable}, got.DecodedRejectionCategories())
}

// The trigger is the rejected row, not the customer on the link: a retry
// that detached the customer and then lost the rest finishes the reset.
func TestAtomicRetryFinishesAResetWhoseCustomerWasAlreadyDetached(t *testing.T) {
	h, _ := newAtomicHarnessWith(t, "sync", &intentStore{journal: new([]string)})
	store := newRetryStore(rejectedAtomicRow(), "")
	store.link.Status = models.IntegrationStatusPending
	wireRetryStore(h, store)
	expectNewTosSession(h)

	got, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")
	require.NoError(t, err)
	require.Len(t, store.detached, 1)
	require.Equal(t, models.KycStatusNotStarted, got.KycStatus)
	require.Nil(t, got.SignedAgreementID)
	require.Nil(t, got.ApplicationDeferredAt)

	// And a second pass finds nothing left to reset.
	again, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")
	require.NoError(t, err)
	require.Len(t, store.detached, 1, "a row already reset is not reset again")
	require.Equal(t, "tos_new", *again.TosSessionID, "the new session is presented again")
}

// A create still active for the person may be linking a customer right now:
// nothing is detached under it.
func TestAtomicRetryWaitsForAnActiveIntent(t *testing.T) {
	for state, want := range map[string]error{
		models.KycIntentUnknown:      kyc.ErrSubmissionInFlight,
		models.KycIntentManualReview: kyc.ErrSubmissionHeld,
	} {
		t.Run(state, func(t *testing.T) {
			intents := &intentStore{journal: new([]string)}
			intents.seed(state, repositories.KycIntentMaterial{Version: 1, SubmissionID: "stg_1"})
			h, _ := newAtomicHarnessWith(t, "sync", intents)
			store := newRetryStore(rejectedAtomicRow(), "cu_old")
			wireRetryStore(h, store)

			_, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")
			require.ErrorIs(t, err, want)
			require.Empty(t, store.detached)
			require.Equal(t, models.KycStatusRejected, store.row.KycStatus)
			h.adapter.AssertNotCalled(t, "CreateTosSession", testifymock.Anything, testifymock.Anything, testifymock.Anything)
		})
	}
}

// The repository's own check — an intent created after the read above — is
// answered the same way.
func TestAtomicRetryAnswersTheRepositorysIntentRefusal(t *testing.T) {
	h, _ := newAtomicHarnessWith(t, "sync", &intentStore{journal: new([]string)})
	store := newRetryStore(rejectedAtomicRow(), "cu_old")
	h.integRepo.EXPECT().DetachCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(false, repositories.ErrActiveKycIntentExists).Once()
	wireRetryStore(h, store)

	_, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")
	require.ErrorIs(t, err, kyc.ErrSubmissionInFlight)
}

// An application sent for a rejected row without the new terms is refused:
// the agreement the row holds belongs to the customer being left.
func TestAtomicApplicationOnARejectedRowRequiresNewTerms(t *testing.T) {
	h, creator := newAtomicHarnessWith(t, "sync", &intentStore{journal: new([]string)})
	store := newRetryStore(rejectedAtomicRow(), "cu_old")
	wireRetryStore(h, store)

	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())
	require.ErrorIs(t, err, ramp.ErrTosRequired)
	require.Equal(t, models.KycStatusRejected, store.row.KycStatus)
	require.NotNil(t, store.row.LastErrorCode, "the refusal is recorded on the row")
	require.Equal(t, "cu_old", customerID(&store.link))
	creator.AssertNotCalled(t, "ValidateDeferredApplication", testifymock.Anything)
}

// The whole retry: rejected → new terms (the old customer detached) → the
// application held again → the documents create a new customer, which links
// over the now empty link without a conflict.
func TestAtomicRetryCreatesAndLinksANewCustomer(t *testing.T) {
	intents := &intentStore{journal: new([]string)}
	h, creator := newAtomicHarnessWith(t, "sync", intents)
	store := newRetryStore(rejectedAtomicRow(), "cu_old")
	wireRetryStore(h, store)
	expectNewTosSession(h)

	_, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")
	require.NoError(t, err)
	// The acceptance the app reports once the hosted page redirects back.
	newAgreement := "to_new"
	store.row.SignedAgreementID = &newAgreement

	creator.EXPECT().ValidateDeferredApplication(testifymock.Anything).Return(deferredState(), nil).Once()
	h.profileRepo.EXPECT().Upsert(uint(7), testifymock.Anything).
		RunAndReturn(func(_ uint, p ramp.Profile) error {
			h.retention.profile = &p
			return nil
		}).Once()
	input := fullApplication()
	input.SignedAgreementID = "to_new"
	input.TaxID, input.Phone, input.Country = "39053344705", "11987654321", "BRA"
	_, err = h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, input)
	require.NoError(t, err)
	require.NotNil(t, store.row.ApplicationDeferredAt)
	require.Equal(t, "to_new", *store.row.SignedAgreementID)

	creator.EXPECT().ValidateAtomicDocuments(testifymock.Anything).Return(nil).Once()
	var staged kyc.StagedSubmission
	h.staging.EXPECT().Put(testifymock.Anything).RunAndReturn(func(s kyc.StagedSubmission) (string, error) {
		staged = s
		return "stg_2", nil
	}).Once()
	h.staging.EXPECT().Get("stg_2").RunAndReturn(func(string) (*kyc.StagedSubmission, error) {
		return &staged, nil
	}).Maybe()
	h.staging.EXPECT().Update("stg_2", testifymock.Anything).Return(nil).Maybe()
	h.staging.EXPECT().Forget("stg_2").Maybe()
	h.dispatcher.EXPECT().Dispatch("stg_2", uint(7), models.ProviderBridge).
		RunAndReturn(func(id string, userID uint, provider string) error {
			return h.svc.ProcessStagedSubmission(id, userID, provider)
		}).Once()
	creator.EXPECT().PrepareCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(atomicPrepared(), nil).Once()
	creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{ExternalCustomerID: "cu_new", Status: models.KycStatusUnderReview}, nil).Once()

	_, err = h.svc.SubmitDocuments(context.Background(), h.user, 7, models.ProviderBridge, atomicDocumentsDTO())
	require.NoError(t, err)

	require.Equal(t, "cu_new", customerID(&store.link), "the new customer is linked")
	require.Equal(t, models.IntegrationStatusActive, store.link.Status)
	require.Equal(t, models.KycStatusUnderReview, store.row.KycStatus)
	require.Nil(t, store.row.ApplicationDeferredAt)
	require.Equal(t, models.KycIntentCompleted, intents.state(), "not held for a customer conflict")
}

// A concurrent call that reset the row first leaves this one nothing to
// detach, and the session it asks for names the link as it now stands — not
// the old customer the link read before the lock still named.
func TestAtomicRetryAfterAConcurrentResetUsesTheLinkAsItStands(t *testing.T) {
	h, _ := newAtomicHarnessWith(t, "sync", &intentStore{journal: new([]string)})
	reset := kycRow(models.KycStatusNotStarted)
	store := newRetryStore(reset, "")
	store.link.Status = models.IntegrationStatusPending
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(rejectedAtomicRow(), nil).Once()
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration("cu_old"), nil).Once()
	wireRetryStore(h, store)
	key := expectNewTosSession(h)

	_, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")
	require.NoError(t, err)
	require.Empty(t, store.detached)
	require.NotRegexp(t, tosIdempotencyKeyPattern, *key,
		"a fresh key though this call did not reset: the derived one may open the old customer's terms")
}

// A link that moved between the read and the detach (ErrLinkConflict) is read
// again, and the detach decided from what it holds now.
func TestAtomicRetryRereadsALinkThatMoved(t *testing.T) {
	h, _ := newAtomicHarnessWith(t, "sync", &intentStore{journal: new([]string)})
	store := newRetryStore(rejectedAtomicRow(), "cu_old")
	h.integRepo.EXPECT().DetachCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(false, repositories.ErrLinkConflict).Once()
	wireRetryStore(h, store)
	expectNewTosSession(h)

	got, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")
	require.NoError(t, err)
	require.Len(t, store.detached, 1, "the second pass detaches")
	require.Equal(t, models.KycStatusNotStarted, got.KycStatus)
}

// A webhook that made the refusal terminal between the read and the detach
// moves the row: the detach misses, the row is read again and refused.
func TestAtomicRetryDoesNotDetachARowTurnedTerminal(t *testing.T) {
	h, _ := newAtomicHarnessWith(t, "sync", &intentStore{journal: new([]string)})
	store := newRetryStore(rejectedAtomicRow(), "cu_old")
	h.integRepo.EXPECT().DetachCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(link *models.CustomerProviderIntegration, row, read *models.CustomerProviderKyc) (bool, error) {
			store.mu.Lock()
			store.row.SetRejectionCategories([]string{models.KycRejectionTaxIDInUse})
			store.mu.Unlock()
			return store.detach(link, row, read)
		}).Once()
	wireRetryStore(h, store)

	_, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")
	require.ErrorIs(t, err, ramp.ErrKycRejected)
	require.Equal(t, "cu_old", customerID(&store.link))
	h.adapter.AssertNotCalled(t, "CreateTosSession", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// Money still able to move at the provider waits the retry out: the old
// customer stays linked, and the refusal is the one an unlink answers.
func TestAtomicRetryWaitsOnMoneyInFlight(t *testing.T) {
	for _, refusal := range []error{repositories.ErrVirtualAccountOpen, repositories.ErrTransactionOpen} {
		t.Run(refusal.Error(), func(t *testing.T) {
			h, _ := newAtomicHarnessWith(t, "sync", &intentStore{journal: new([]string)})
			store := newRetryStore(rejectedAtomicRow(), "cu_old")
			h.integRepo.EXPECT().DetachCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
				Return(false, fmt.Errorf("user provider integration repo: detach customer: %w", refusal)).Once()
			wireRetryStore(h, store)

			_, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")
			require.ErrorIs(t, err, refusal)
			want := kyc.ErrVirtualAccountActive
			if refusal == repositories.ErrTransactionOpen {
				want = kyc.ErrTransactionOpen
			}
			require.ErrorIs(t, err, want)
			require.Equal(t, "cu_old", customerID(&store.link))
			h.adapter.AssertNotCalled(t, "CreateTosSession", testifymock.Anything, testifymock.Anything, testifymock.Anything)
		})
	}
}
