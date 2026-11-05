package kyc_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

// intentStore is an in-memory intent repository: it keeps the one active
// intent the worker reads and writes, applies each write the way the real
// repository does, and logs every call into a journal the dispatcher writes to
// as well, so a test can assert what happened before what.
type intentStore struct {
	mu       sync.Mutex
	intent   *models.ProviderKycSubmissionIntent
	material *repositories.KycIntentMaterial
	findErr  error
	journal  *[]string
	// created is every material Create was handed.
	created []repositories.KycIntentMaterial
}

var _ repositories.ProviderKycSubmissionIntentRepository = (*intentStore)(nil)

func (s *intentStore) note(call string) { *s.journal = append(*s.journal, call) }

func (s *intentStore) Create(intent *models.ProviderKycSubmissionIntent, material repositories.KycIntentMaterial) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("intent.Create")
	if intent.ExternalID == "" {
		intent.ExternalID = uuid.NewString()
	}
	if intent.ProviderIdempotencyKey == "" {
		intent.ProviderIdempotencyKey = repositories.KycIntentKeyPrefix + intent.ExternalID
	}
	intent.ID = 1
	intent.State = models.KycIntentPreparing
	intent.Revision = 1
	s.created = append(s.created, copyMaterial(material))
	stored := *intent
	s.intent, s.material = &stored, ptrMaterial(copyMaterial(material))
	return nil
}

// copyMaterial is a deep copy, as a read from the database is: the worker
// must never share a slice or a pointer with what the repository holds.
func copyMaterial(m repositories.KycIntentMaterial) repositories.KycIntentMaterial {
	m.Checkpoint.Fields = append([]ramp.AtomicKycUploadedField(nil), m.Checkpoint.Fields...)
	if m.Prepared != nil {
		prepared := *m.Prepared
		prepared.Payload = append(json.RawMessage(nil), m.Prepared.Payload...)
		m.Prepared = &prepared
	}
	return m
}

func ptrMaterial(m repositories.KycIntentMaterial) *repositories.KycIntentMaterial { return &m }

func (s *intentStore) FindActive(uint, string) (*models.ProviderKycSubmissionIntent, *repositories.KycIntentMaterial, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("intent.FindActive")
	if s.findErr != nil {
		if s.intent == nil {
			return nil, nil, s.findErr
		}
		intent := *s.intent
		return &intent, nil, s.findErr
	}
	if s.intent == nil {
		return nil, nil, nil
	}
	active := false
	for _, state := range models.ActiveKycIntentStates {
		active = active || state == s.intent.State
	}
	if !active {
		return nil, nil, nil
	}
	intent := *s.intent
	return &intent, ptrMaterial(copyMaterial(*s.material)), nil
}

func (s *intentStore) FindBySubmission(_ uint, _ string, submissionID string) (*models.ProviderKycSubmissionIntent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("intent.FindBySubmission")
	if s.intent == nil || s.intent.SubmissionID != submissionID {
		return nil, nil
	}
	intent := *s.intent
	return &intent, nil
}

func (s *intentStore) FindByExternalID(string) (*models.ProviderKycSubmissionIntent, *repositories.KycIntentMaterial, error) {
	panic("not used")
}

// write applies change under the revision guard, as the repository does: it
// lands only on the revision the caller read, and then bumps it on both
// copies; otherwise it is ErrStaleKycIntent and nothing changes.
func (s *intentStore) write(call string, intent *models.ProviderKycSubmissionIntent, change func(*models.ProviderKycSubmissionIntent)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.intent == nil || intent.Revision != s.intent.Revision {
		s.note(call + " (stale)")
		return repositories.ErrStaleKycIntent
	}
	s.note(call)
	next := *intent
	change(&next)
	next.Revision++
	*intent = next
	s.intent = &next
	return nil
}

// claim is the sweeper claiming the intent: a lease, a counted attempt and a
// new revision, written behind the worker's back.
func (s *intentStore) claim(leaseUntil time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("sweeper.Claim")
	next := *s.intent
	next.RecoveryLeaseExpiresAt = &leaseUntil
	next.RecoveryNextAttemptAt = nil
	next.RecoveryAttempts++
	next.Revision++
	s.intent = &next
}

func (s *intentStore) Checkpoint(intent *models.ProviderKycSubmissionIntent, material repositories.KycIntentMaterial) error {
	err := s.write("intent.Checkpoint", intent, func(*models.ProviderKycSubmissionIntent) {})
	if err == nil {
		s.material = ptrMaterial(copyMaterial(material))
	}
	return err
}

func (s *intentStore) MarkPrepared(intent *models.ProviderKycSubmissionIntent, material repositories.KycIntentMaterial) error {
	err := s.write("intent.MarkPrepared", intent, func(i *models.ProviderKycSubmissionIntent) {
		i.State = models.KycIntentPrepared
		digest := material.Prepared.PayloadSHA256
		i.BodySHA256 = &digest
	})
	if err == nil {
		s.material = ptrMaterial(copyMaterial(material))
	}
	return err
}

func (s *intentStore) BeginSubmitting(intent *models.ProviderKycSubmissionIntent, now, leaseUntil time.Time) error {
	return s.write("intent.BeginSubmitting", intent, func(i *models.ProviderKycSubmissionIntent) {
		i.State = models.KycIntentSubmitting
		i.AttemptCount++
		i.LastAttemptAt = &now
		i.RecoveryLeaseExpiresAt = &leaseUntil
		i.RecoveryNextAttemptAt = nil
	})
}

func (s *intentStore) RevertAttempt(intent *models.ProviderKycSubmissionIntent, before models.ProviderKycSubmissionIntent) error {
	return s.write("intent.RevertAttempt", intent, func(i *models.ProviderKycSubmissionIntent) {
		i.State, i.AttemptCount, i.LastAttemptAt = before.State, before.AttemptCount, before.LastAttemptAt
	})
}

func (s *intentStore) MarkUnknown(intent *models.ProviderKycSubmissionIntent, code string) error {
	return s.write("intent.MarkUnknown", intent, func(i *models.ProviderKycSubmissionIntent) {
		i.State = models.KycIntentUnknown
		i.FailureCode = &code
	})
}

func (s *intentStore) RecordCustomer(intent *models.ProviderKycSubmissionIntent, customerID string) error {
	return s.write("intent.RecordCustomer", intent, func(i *models.ProviderKycSubmissionIntent) {
		i.ProviderCustomerID = &customerID
	})
}

func (s *intentStore) Complete(intent *models.ProviderKycSubmissionIntent, now time.Time) error {
	return s.write("intent.Complete", intent, func(i *models.ProviderKycSubmissionIntent) {
		i.State = models.KycIntentCompleted
		i.RecoveryNextAttemptAt, i.RecoveryLeaseExpiresAt = nil, nil
	})
}

func (s *intentStore) MarkFailed(intent *models.ProviderKycSubmissionIntent, code string, now time.Time) error {
	return s.write("intent.MarkFailed", intent, func(i *models.ProviderKycSubmissionIntent) {
		i.State = models.KycIntentFailed
		i.FailureCode = &code
	})
}

func (s *intentStore) MarkManualReview(intent *models.ProviderKycSubmissionIntent, code string, retainUntil time.Time) error {
	return s.write("intent.MarkManualReview", intent, func(i *models.ProviderKycSubmissionIntent) {
		i.State = models.KycIntentManualReview
		i.FailureCode = &code
		i.MaterialRetainUntil = &retainUntil
	})
}

func (s *intentStore) ScheduleRecovery(intent *models.ProviderKycSubmissionIntent, at time.Time, code string) error {
	return s.write("intent.ScheduleRecovery", intent, func(i *models.ProviderKycSubmissionIntent) {
		i.RecoveryNextAttemptAt = &at
		i.RecoveryLeaseExpiresAt = nil
		if code != "" {
			i.FailureCode = &code
		}
	})
}

// ClaimRecoveryDue claims the one intent when it is recoverable and due, the
// way the repository does: a lease and a counted attempt, last_attempt_at
// left alone.
func (s *intentStore) ClaimRecoveryDue(now, leaseUntil time.Time, _, _ int, _ time.Time) ([]repositories.ClaimedKycIntent, error) {
	s.mu.Lock()
	due := false
	if s.intent != nil {
		for _, state := range models.RecoverableKycIntentStates {
			due = due || state == s.intent.State
		}
		next, lease := s.intent.RecoveryNextAttemptAt, s.intent.RecoveryLeaseExpiresAt
		due = due && (next != nil && !next.After(now) || lease != nil && !lease.After(now))
	}
	s.mu.Unlock()
	if !due {
		return nil, nil
	}
	s.claim(leaseUntil)
	s.mu.Lock()
	defer s.mu.Unlock()
	return []repositories.ClaimedKycIntent{{Intent: *s.intent, Material: ptrMaterial(copyMaterial(*s.material))}}, nil
}

func (s *intentStore) Resolve(string, string, string, time.Time) error { panic("not used") }

func (s *intentStore) ListManualReview(int) ([]models.ProviderKycSubmissionIntent, error) {
	panic("not used")
}

func (s *intentStore) ScrubExpiredMaterial(time.Time, int) (int, error) { panic("not used") }

// state is the stored intent's state, "" when there is none.
func (s *intentStore) state() string {
	if s.intent == nil {
		return ""
	}
	return s.intent.State
}

func (s *intentStore) failureCode() string {
	if s.intent == nil || s.intent.FailureCode == nil {
		return ""
	}
	return *s.intent.FailureCode
}

// seed stores an active intent for stg_1 at state, with its material.
func (s *intentStore) seed(state string, material repositories.KycIntentMaterial) *models.ProviderKycSubmissionIntent {
	external := uuid.NewString()
	s.intent = &models.ProviderKycSubmissionIntent{
		ExternalID: external, CustomerID: 7, Provider: models.ProviderBridge, SubmissionID: "stg_1",
		State: state, ProviderIdempotencyKey: repositories.KycIntentKeyPrefix + external,
		RetryDeadline: time.Now().Add(20 * time.Hour), Revision: 3,
	}
	s.intent.ID = 1
	s.material = ptrMaterial(copyMaterial(material))
	return s.intent
}

// --- fixtures ---------------------------------------------------------------

const atomicClientIP = "203.0.113.7"

func atomicDocuments() ramp.Documents {
	return ramp.Documents{
		IdentifyingInformation: []ramp.IdentifyingDocument{{
			Type: "national_id", IssuingCountry: "BRA", ImageFront: "data:image/jpeg;base64,RlJPTlQ=",
		}},
		LivenessCheckSelfies: []ramp.Selfie{{Image: "data:image/jpeg;base64,U0VMRklF"}},
		ClientIPAddress:      atomicClientIP,
	}
}

func atomicPrepared() *ramp.PreparedAtomicKyc {
	return &ramp.PreparedAtomicKyc{
		Version: 1, Payload: json.RawMessage(`{"first_name":"Luiza","ip_address":"203.0.113.7"}`),
		PayloadSHA256: "5a1d",
	}
}

// deferredRow is the row once the documents were staged: in flight, with the
// application held since before and the agreement the terms step captured.
func deferredRow() *models.CustomerProviderKyc {
	row := kycRow(models.KycStatusSubmitting)
	deferredAt := time.Now().Add(-time.Minute)
	row.ApplicationDeferredAt = &deferredAt
	agreement := "sa_1"
	row.SignedAgreementID = &agreement
	row.TosStatus = models.TosStatusApproved
	return row
}

// atomicWorker is the harness for the worker half of an atomic create.
type atomicWorker struct {
	h       *harness
	creator *mocks.MockAtomicKycCreator
	store   *intentStore
	journal []string
	row     *models.CustomerProviderKyc
	link    *models.CustomerProviderIntegration
}

func newAtomicWorker(t *testing.T, queueDriver string) *atomicWorker {
	t.Helper()
	w := &atomicWorker{row: deferredRow(), link: linkedIntegration("")}
	w.store = &intentStore{journal: &w.journal}
	w.h, w.creator = newAtomicHarnessWith(t, queueDriver, w.store)
	w.h.retention.profile = &ramp.Profile{
		FirstName: "Luiza", LastName: "Souza", Email: "luiza@example.com", Phone: "+5511987654321",
		TaxID: "39053344705", TaxIDType: models.TaxIDTypeCPF, TaxIDCountry: "BRA",
	}
	w.h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(w.link, nil).Maybe()
	w.h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		RunAndReturn(func(uint, string) (*models.CustomerProviderKyc, error) {
			row := *w.row
			return &row, nil
		}).Maybe()
	return w
}

// staged answers the staging read with the documents, attempts self-dispatches in.
func (w *atomicWorker) staged(attempts int) {
	docs := atomicDocuments()
	w.h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindAtomicCustomer,
		PreviousStatus: models.KycStatusIncomplete, StagedAt: time.Now(),
		Documents: &docs, DispatchAttempts: attempts,
	}, nil).Once()
}

// expectPrepare answers the prepare once, uploading one file through persist.
func (w *atomicWorker) expectPrepare(t *testing.T) {
	w.h.staging.EXPECT().Update("stg_1", testifymock.Anything).Return(nil).Maybe()
	w.creator.EXPECT().PrepareCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(_ context.Context, sub ramp.AtomicKycSubmission, cp ramp.AtomicKycCheckpoint,
			persist func(ramp.AtomicKycCheckpoint) error,
		) (*ramp.PreparedAtomicKyc, error) {
			// The checkpoint a first run starts from is one the adapter accepts:
			// the zero value, or its own version for this very submission.
			require.True(t, cp.Version == 0 && cp.SubmissionID == "" && len(cp.Fields) == 0 ||
				cp.Version == 1 && cp.SubmissionID == sub.SubmissionID, "checkpoint %+v", cp)
			require.NoError(t, persist(ramp.AtomicKycCheckpoint{
				Version: 1, SubmissionID: sub.SubmissionID,
				Fields: []ramp.AtomicKycUploadedField{{Field: "id_doc_front_file", DocumentSHA256: "f0", ProviderHandle: "h_front"}},
			}))
			return atomicPrepared(), nil
		}).Once()
}

// expectLinked answers the link-and-row write and keeps what was written.
func (w *atomicWorker) expectLinked() **models.CustomerProviderKyc {
	var saved *models.CustomerProviderKyc
	w.h.kycRepo.EXPECT().SaveWithLink(testifymock.Anything, false, w.link, models.KycStatusSubmitting).
		RunAndReturn(func(row *models.CustomerProviderKyc, _ bool, _ *models.CustomerProviderIntegration, _ string) (bool, error) {
			saved = row
			return true, nil
		}).Once()
	return &saved
}

// expectReleased answers the release of the row and keeps what was written.
func (w *atomicWorker) expectReleased() **models.CustomerProviderKyc {
	var saved *models.CustomerProviderKyc
	w.h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusSubmitting).
		RunAndReturn(func(row *models.CustomerProviderKyc, _ string) (bool, error) {
			saved = row
			return true, nil
		}).Once()
	w.h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.FailureReason != ""
	})).Once()
	return &saved
}

func (w *atomicWorker) run(t *testing.T) {
	t.Helper()
	require.NoError(t, w.h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))
}

// --- the request half -------------------------------------------------------

// The documents are what create the customer, from the application held
// until they came. With nothing held there is nothing to create it from.
func TestAtomicDocumentsWithoutADeferredApplicationAreRefused(t *testing.T) {
	h, creator := newAtomicHarness(t)
	creator.EXPECT().ValidateAtomicDocuments(testifymock.Anything).Return(nil).Once()
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusNotStarted), nil).Once()

	_, err := h.svc.SubmitDocuments(context.Background(), h.user, 7, models.ProviderBridge, atomicDocumentsDTO())
	require.ErrorIs(t, err, kyc.ErrApplicationRequired)
	h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
}

// A bad image or a missing IP is a 422 in the request, before anything is
// linked or staged: the job could only fail on it later.
func TestAtomicDocumentsAreValidatedBeforeStaging(t *testing.T) {
	h, creator := newAtomicHarness(t)
	creator.EXPECT().ValidateAtomicDocuments(testifymock.MatchedBy(func(d ramp.Documents) bool {
		return d.ClientIPAddress == atomicClientIP
	})).Return(fmt.Errorf("blindpay: selfie: %w", ramp.ErrKycDataInvalid)).Once()

	_, err := h.svc.SubmitDocuments(context.Background(), h.user, 7, models.ProviderBridge, atomicDocumentsDTO())
	require.ErrorIs(t, err, ramp.ErrKycDataInvalid)
	h.linker.AssertNotCalled(t, "Start", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
}

// Documents for a held application are staged as the customer's create.
func TestAtomicDocumentsAreStagedAsTheCustomerCreate(t *testing.T) {
	store := &intentStore{journal: new([]string)}
	h, creator := newAtomicHarnessWith(t, "redis", store)
	creator.EXPECT().ValidateAtomicDocuments(testifymock.Anything).Return(nil).Once()
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	row := deferredRow()
	row.KycStatus = models.KycStatusIncomplete
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	h.staging.EXPECT().Put(testifymock.MatchedBy(func(s kyc.StagedSubmission) bool {
		return s.Kind == kyc.KindAtomicCustomer && s.PreviousStatus == models.KycStatusIncomplete &&
			s.Documents != nil && s.Documents.ClientIPAddress == atomicClientIP
	})).Return("stg_1", nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(row, models.KycStatusIncomplete).Return(true, nil).Once()
	h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).Return(nil).Once()

	got, err := h.svc.SubmitDocuments(context.Background(), h.user, 7, models.ProviderBridge, atomicDocumentsDTO())
	require.NoError(t, err)
	require.Equal(t, models.KycStatusSubmitting, got.KycStatus)
}

// The customer of a provider that takes the whole person in one call is
// born whole: nothing can be added to it. Documents for one already linked
// are refused up front, and nothing is staged for a job to fail on.
func TestAtomicDocumentsForALinkedCustomerAreRefused(t *testing.T) {
	store := &intentStore{journal: new([]string)}
	h, creator := newAtomicHarnessWith(t, "redis", store)
	creator.EXPECT().ValidateAtomicDocuments(testifymock.Anything).Return(nil).Once()
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_bp"), nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration("cus_bp"), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusUnderReview), nil).Once()

	_, err := h.svc.SubmitDocuments(context.Background(), h.user, 7, models.ProviderBridge, atomicDocumentsDTO())
	require.ErrorIs(t, err, ramp.ErrDocumentsNotSupported)
	h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
	h.dispatcher.AssertNotCalled(t, "Dispatch", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// One create per person: documents arriving while an intent is still active
// are a second submission, not a retry of the first.
func TestAtomicDocumentsWhileAnIntentIsActiveAreRefused(t *testing.T) {
	store := &intentStore{journal: new([]string)}
	store.seed(models.KycIntentUnknown, repositories.KycIntentMaterial{Version: 1, SubmissionID: "stg_1"})
	h, creator := newAtomicHarnessWith(t, "redis", store)
	creator.EXPECT().ValidateAtomicDocuments(testifymock.Anything).Return(nil).Once()
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	row := deferredRow()
	row.KycStatus = models.KycStatusIncomplete
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()

	_, err := h.svc.SubmitDocuments(context.Background(), h.user, 7, models.ProviderBridge, atomicDocumentsDTO())
	require.ErrorIs(t, err, kyc.ErrSubmissionInFlight)
	h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
}

// The documents fan-out skips a provider with no customer and nothing in
// flight — but a held application is what the documents of an atomic
// provider complete, so that row receives them.
func TestAtomicDocumentsFanOutReachesADeferredRow(t *testing.T) {
	store := &intentStore{journal: new([]string)}
	h, creator := newAtomicHarnessWith(t, "redis", store)
	row := deferredRow()
	row.KycStatus = models.KycStatusIncomplete
	row.SetRequirements([]string{"government_id", "selfie"})
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderKyc{*row}, nil).Once()
	h.integRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderIntegration{*linkedIntegration("")}, nil).Once()

	creator.EXPECT().ValidateAtomicDocuments(testifymock.Anything).Return(nil).Once()
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	h.staging.EXPECT().Put(testifymock.MatchedBy(func(s kyc.StagedSubmission) bool {
		return s.Kind == kyc.KindAtomicCustomer
	})).Return("stg_1", nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusIncomplete).Return(true, nil).Once()
	h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).Return(nil).Once()

	outcomes, err := h.svc.SubmitDocumentsToEnabled(context.Background(), h.user, 7, atomicDocumentsDTO())
	require.NoError(t, err)
	require.Len(t, outcomes, 1)
	require.True(t, outcomes[0].Accepted(), "skipped: %s", outcomes[0].SkipReason)
}

func atomicDocumentsDTO() dto.DocumentsDTO {
	return dto.DocumentsDTO{
		DocumentType: "national_id", IssuingCountry: "BRA",
		ImageFront: "data:image/jpeg;base64,RlJPTlQ=", Selfie: "data:image/jpeg;base64,U0VMRklF",
		ClientIPAddress: atomicClientIP,
	}
}

// --- the worker half --------------------------------------------------------

// The whole create: an intent, the prepare checkpointed into it, the body
// sent with the intent's key, the customer linked and the intent completed.
func TestAtomicHappyPath(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	w.staged(0)
	w.expectPrepare(t)
	w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, *atomicPrepared()).
		RunAndReturn(func(_ context.Context, key string, _ ramp.PreparedAtomicKyc) (*ramp.KycState, error) {
			require.Equal(t, w.store.intent.ProviderIdempotencyKey, key)
			return &ramp.KycState{ExternalCustomerID: "cus_bp"}, nil
		}).Once()
	saved := w.expectLinked()
	w.h.staging.EXPECT().Forget("stg_1").Once()
	w.h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.KycStatus == models.KycStatusUnderReview && e.FailureReason == ""
	})).Once()

	w.run(t)

	require.Equal(t, []string{
		"intent.FindActive", "intent.FindBySubmission", "intent.Create", "intent.Checkpoint", "intent.MarkPrepared",
		"intent.BeginSubmitting", "intent.RecordCustomer", "intent.Complete",
	}, w.journal)
	require.Len(t, w.store.created, 1)
	first := w.store.created[0]
	require.Equal(t, "stg_1", first.SubmissionID)
	require.Equal(t, atomicClientIP, first.ClientIPAddress)
	require.Equal(t, ramp.AtomicKycCheckpoint{}, first.Checkpoint, "a first run starts from the zero checkpoint")
	require.Equal(t, models.KycIntentCompleted, w.store.state())
	require.Equal(t, "cus_bp", *w.store.intent.ProviderCustomerID)
	require.Equal(t, 1, w.store.intent.AttemptCount)
	require.WithinDuration(t, time.Now().Add(24*time.Hour), w.store.intent.RetryDeadline, time.Minute)

	require.NotNil(t, *saved)
	require.Equal(t, models.KycStatusUnderReview, (*saved).KycStatus)
	require.Nil(t, (*saved).ApplicationDeferredAt, "the customer exists: nothing is held any more")
	require.Equal(t, "cus_bp", *w.link.ExternalCustomerID)
}

// A stale delivery of a create that already finished — the job delivered
// twice, its staging still there — finds no active intent. It must not open a
// second one: under a new key BlindPay would make a second customer. The link
// already names the customer, so the run drops the staging and stops, and the
// row, which the first run settled, is left as it is.
func TestAtomicStaleDeliveryAfterTheCustomerIsLinkedCreatesNothing(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	customer := "cus_bp"
	w.link.ExternalCustomerID = &customer
	w.row = kycRow(models.KycStatusUnderReview)
	w.staged(0)
	w.h.staging.EXPECT().Forget("stg_1").Once()

	w.run(t)

	require.NotContains(t, w.journal, "intent.Create")
	require.Empty(t, w.store.created)
	w.creator.AssertNotCalled(t, "PrepareCustomer",
		testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
	w.creator.AssertNotCalled(t, "CreatePreparedCustomer", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	w.h.kycRepo.AssertNotCalled(t, "UpdateIfStatus", testifymock.Anything, testifymock.Anything)
}

// The same delivery while the submission's own intent has ended — completed,
// failed, resolved by an operator: that intent was this submission's one
// create, and no second is opened for it.
func TestAtomicStaleDeliveryOfAnEndedIntentCreatesNothing(t *testing.T) {
	for _, state := range []string{models.KycIntentCompleted, models.KycIntentFailed, models.KycIntentResolved} {
		t.Run(state, func(t *testing.T) {
			w := newAtomicWorker(t, "redis")
			w.store.seed(state, repositories.KycIntentMaterial{Version: 1, SubmissionID: "stg_1"})
			revision := w.store.intent.Revision
			w.staged(0)
			w.h.staging.EXPECT().Forget("stg_1").Once()

			w.run(t)

			require.Equal(t, []string{"intent.FindActive", "intent.FindBySubmission"}, w.journal)
			require.Equal(t, revision, w.store.intent.Revision, "the ended intent is untouched")
			w.creator.AssertNotCalled(t, "PrepareCustomer",
				testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
			w.h.kycRepo.AssertNotCalled(t, "UpdateIfStatus", testifymock.Anything, testifymock.Anything)
		})
	}
}

// The prepare is built from the retained profile and the row's agreement,
// and the intent's external id is the reference the provider files it under.
func TestAtomicPrepareIsBuiltFromTheRetainedProfile(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	w.staged(0)
	w.h.staging.EXPECT().Update("stg_1", testifymock.Anything).Return(nil).Maybe()
	w.creator.EXPECT().PrepareCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(_ context.Context, sub ramp.AtomicKycSubmission, _ ramp.AtomicKycCheckpoint,
			_ func(ramp.AtomicKycCheckpoint) error,
		) (*ramp.PreparedAtomicKyc, error) {
			require.Equal(t, "stg_1", sub.SubmissionID)
			require.Equal(t, w.store.intent.ExternalID, sub.Reference)
			require.Equal(t, "individual", sub.Application.Type)
			require.Equal(t, "sa_1", sub.Application.SignedAgreementID)
			require.Equal(t, "Luiza", sub.Application.Profile.FirstName)
			require.Equal(t, []ramp.IdentifyingDocument{{Type: models.TaxIDTypeCPF, IssuingCountry: "BRA", Number: "39053344705"}},
				sub.Application.IdentifyingInformation)
			require.Equal(t, atomicClientIP, sub.Documents.ClientIPAddress)
			return nil, fmt.Errorf("blindpay: selfie: %w", ramp.ErrKycDataInvalid)
		}).Once()
	w.h.staging.EXPECT().Forget("stg_1").Once()
	saved := w.expectReleased()

	w.run(t)

	require.Equal(t, models.KycIntentFailed, w.store.state())
	require.Equal(t, models.KycStatusIncomplete, (*saved).KycStatus)
	require.Equal(t, "kyc_data_invalid", *(*saved).LastErrorCode)
}

// A create whose outcome is unknown is sent again with the very bytes and the
// very key of the first: the second run does not prepare again.
func TestAtomicRetryReusesTheStoredBody(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	w.staged(0)
	w.expectPrepare(t)
	var keys []string
	var bodies []json.RawMessage
	w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(_ context.Context, key string, p ramp.PreparedAtomicKyc) (*ramp.KycState, error) {
			keys, bodies = append(keys, key), append(bodies, p.Payload)
			return nil, fmt.Errorf("blindpay: 503: %w", ramp.ErrRetryableUpstream)
		}).Once()
	w.h.dispatcher.EXPECT().DispatchAfter("stg_1", uint(7), models.ProviderBridge, time.Second).
		RunAndReturn(func(string, uint, string, time.Duration) error {
			w.journal = append(w.journal, "dispatcher.DispatchAfter")
			return nil
		}).Once()

	w.run(t)

	require.Equal(t, []string{
		"intent.FindActive", "intent.FindBySubmission", "intent.Create", "intent.Checkpoint", "intent.MarkPrepared",
		"intent.BeginSubmitting", "intent.MarkUnknown", "intent.ScheduleRecovery", "dispatcher.DispatchAfter",
	}, w.journal)
	require.Equal(t, models.KycIntentUnknown, w.store.state())
	require.Equal(t, "create_outcome_unknown", w.store.failureCode())
	require.NotNil(t, w.store.material.Prepared)
	w.h.staging.AssertNotCalled(t, "Forget", "stg_1")
	w.h.kycRepo.AssertNotCalled(t, "UpdateIfStatus", testifymock.Anything, testifymock.Anything)

	// The second run: same submission, its intent prepared.
	w.journal = nil
	w.staged(1)
	w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(_ context.Context, key string, p ramp.PreparedAtomicKyc) (*ramp.KycState, error) {
			keys, bodies = append(keys, key), append(bodies, p.Payload)
			return &ramp.KycState{ExternalCustomerID: "cus_bp"}, nil
		}).Once()
	w.expectLinked()
	w.h.staging.EXPECT().Forget("stg_1").Once()
	w.h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	w.run(t)

	require.Equal(t, []string{
		"intent.FindActive", "intent.BeginSubmitting", "intent.RecordCustomer", "intent.Complete",
	}, w.journal)
	require.Len(t, keys, 2)
	require.Equal(t, keys[0], keys[1], "the same key")
	require.Equal(t, string(bodies[0]), string(bodies[1]), "the same bytes")
	require.Equal(t, 2, w.store.intent.AttemptCount)
	require.Equal(t, models.KycIntentCompleted, w.store.state())
}

// A create that got no answer back — the client timed out, the deadline
// passed, the connection broke after the request was on it — may have made
// the customer. It is never held for that: the intent is left unknown and
// rescheduled, and the replay sends the very key and bytes of the first.
func TestAtomicCreateWithNoAnswerIsReplayed(t *testing.T) {
	for name, noAnswer := range map[string]error{
		"client timeout": fmt.Errorf("blindpay: POST customers: %w: %w", ramp.ErrOutcomeUnknown,
			errors.New("Client.Timeout exceeded while awaiting headers")),
		"deadline": fmt.Errorf("blindpay: POST customers: %w", context.DeadlineExceeded),
	} {
		t.Run(name, func(t *testing.T) {
			w := newAtomicWorker(t, "redis")
			w.store.seed(models.KycIntentPrepared, repositories.KycIntentMaterial{
				Version: 1, SubmissionID: "stg_1", ClientIPAddress: atomicClientIP, Prepared: atomicPrepared(),
			})
			w.staged(0)
			var keys []string
			var bodies []json.RawMessage
			w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
				RunAndReturn(func(_ context.Context, key string, p ramp.PreparedAtomicKyc) (*ramp.KycState, error) {
					keys, bodies = append(keys, key), append(bodies, p.Payload)
					return nil, noAnswer
				}).Once()
			w.h.staging.EXPECT().Update("stg_1", testifymock.Anything).Return(nil).Once()
			w.h.dispatcher.EXPECT().DispatchAfter("stg_1", uint(7), models.ProviderBridge, time.Second).Return(nil).Once()

			w.run(t)

			require.Equal(t, models.KycIntentUnknown, w.store.state())
			require.Equal(t, "create_outcome_unknown", w.store.failureCode())
			require.NotNil(t, w.store.intent.RecoveryNextAttemptAt, "rescheduled")
			w.h.kycRepo.AssertNotCalled(t, "UpdateIfStatus", testifymock.Anything, testifymock.Anything)
			w.h.staging.AssertNotCalled(t, "Forget", "stg_1")

			w.staged(1)
			w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
				RunAndReturn(func(_ context.Context, key string, p ramp.PreparedAtomicKyc) (*ramp.KycState, error) {
					keys, bodies = append(keys, key), append(bodies, p.Payload)
					return &ramp.KycState{ExternalCustomerID: "cus_bp"}, nil
				}).Once()
			w.expectLinked()
			w.h.staging.EXPECT().Forget("stg_1").Once()
			w.h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

			w.run(t)

			require.Equal(t, models.KycIntentCompleted, w.store.state())
			require.Len(t, keys, 2)
			require.Equal(t, keys[0], keys[1], "the same key")
			require.Equal(t, string(bodies[0]), string(bodies[1]), "the same bytes")
		})
	}
}

// The worker's own re-queue backs off, and after eight of them it stops: the
// intent waits for the sweeper, and the row is let go.
func TestAtomicSelfDispatchBacksOffAndStops(t *testing.T) {
	for attempts, delay := range map[int]time.Duration{0: time.Second, 3: 8 * time.Second, 7: 64 * time.Second} {
		t.Run(fmt.Sprintf("attempt %d", attempts), func(t *testing.T) {
			w := newAtomicWorker(t, "redis")
			w.store.seed(models.KycIntentUnknown, repositories.KycIntentMaterial{
				Version: 1, SubmissionID: "stg_1", ClientIPAddress: atomicClientIP, Prepared: atomicPrepared(),
			})
			w.staged(attempts)
			w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
				Return(nil, ramp.ErrRetryableUpstream).Once()
			w.h.staging.EXPECT().Update("stg_1", testifymock.MatchedBy(func(s kyc.StagedSubmission) bool {
				return s.DispatchAttempts == attempts+1
			})).Return(nil).Once()
			w.h.dispatcher.EXPECT().DispatchAfter("stg_1", uint(7), models.ProviderBridge, delay).Return(nil).Once()

			before := time.Now()
			w.run(t)
			require.WithinRange(t, *w.store.intent.RecoveryNextAttemptAt,
				before.Add(delay+150*time.Second), time.Now().Add(delay+150*time.Second))
		})
	}

	t.Run("after eight", func(t *testing.T) {
		w := newAtomicWorker(t, "redis")
		w.store.seed(models.KycIntentUnknown, repositories.KycIntentMaterial{
			Version: 1, SubmissionID: "stg_1", ClientIPAddress: atomicClientIP, Prepared: atomicPrepared(),
		})
		w.staged(8)
		w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
			Return(nil, ramp.ErrRetryableUpstream).Once()
		saved := w.expectReleased()

		before := time.Now()
		w.run(t)

		w.h.dispatcher.AssertNotCalled(t, "DispatchAfter",
			testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
		require.Equal(t, models.KycIntentUnknown, w.store.state())
		require.WithinRange(t, *w.store.intent.RecoveryNextAttemptAt,
			before.Add(150*time.Second), time.Now().Add(150*time.Second))
		require.Equal(t, models.KycStatusIncomplete, (*saved).KycStatus)
		require.Equal(t, "provider_busy", *(*saved).LastErrorCode)
	})
}

// The create landed but reading the customer back failed: the customer is
// linked anyway and the intent is done — a replay would only find it again.
func TestAtomicCreateSucceededButReadFailed(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	w.store.seed(models.KycIntentPrepared, repositories.KycIntentMaterial{
		Version: 1, SubmissionID: "stg_1", ClientIPAddress: atomicClientIP, Prepared: atomicPrepared(),
	})
	w.staged(0)
	w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{ExternalCustomerID: "cus_bp"}, fmt.Errorf("blindpay: read customer: %w", ramp.ErrProviderFailed)).Once()
	saved := w.expectLinked()
	w.h.staging.EXPECT().Forget("stg_1").Once()
	w.h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	w.run(t)

	require.Equal(t, models.KycIntentCompleted, w.store.state())
	require.Equal(t, "cus_bp", *w.store.intent.ProviderCustomerID)
	require.Equal(t, "cus_bp", *w.link.ExternalCustomerID)
	require.Equal(t, models.KycStatusUnderReview, (*saved).KycStatus)
	require.Nil(t, (*saved).ApplicationDeferredAt)
}

// A refusal is the answer: the intent fails and the row is released with it.
func TestAtomicRefusedCreateFailsTheIntent(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	w.store.seed(models.KycIntentPrepared, repositories.KycIntentMaterial{
		Version: 1, SubmissionID: "stg_1", ClientIPAddress: atomicClientIP, Prepared: atomicPrepared(),
	})
	w.staged(0)
	w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, fmt.Errorf("blindpay: 400: %w", ramp.ErrKycDataInvalid)).Once()
	w.h.staging.EXPECT().Forget("stg_1").Once()
	saved := w.expectReleased()

	w.run(t)

	require.Equal(t, models.KycIntentFailed, w.store.state())
	require.Equal(t, models.KycStatusIncomplete, (*saved).KycStatus)
	require.Equal(t, "kyc_data_invalid", *(*saved).LastErrorCode)
	// BlindPay refused what the create carried — the held application as much
	// as the documents — so the application is asked for again.
	require.Nil(t, (*saved).ApplicationDeferredAt, "the held application is let go with the refusal")
	rows := []models.CustomerProviderKyc{**saved}
	kyc.MarkNeedsApplication(rows, nil, nil)
	require.True(t, rows[0].NeedsApplication, "the app asks for the application again")
}

// A create that failed for any other reason keeps the application held: only
// the documents are owed again.
func TestAtomicCreateFailureOtherThanTheDataKeepsTheApplication(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	w.store.seed(models.KycIntentPrepared, repositories.KycIntentMaterial{
		Version: 1, SubmissionID: "stg_1", ClientIPAddress: atomicClientIP, Prepared: atomicPrepared(),
	})
	w.staged(0)
	w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, fmt.Errorf("blindpay: 400: %w", ramp.ErrTosRequired)).Once()
	w.h.staging.EXPECT().Forget("stg_1").Once()
	saved := w.expectReleased()

	w.run(t)

	require.Equal(t, models.KycIntentFailed, w.store.state())
	require.Equal(t, "tos_required", *(*saved).LastErrorCode)
	require.NotNil(t, (*saved).ApplicationDeferredAt)
}

// Another submission owns the active intent: this one is in flight behind it,
// and never reaches the provider.
func TestAtomicSecondSubmissionWhileOneIsActive(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	intent := w.store.seed(models.KycIntentUnknown, repositories.KycIntentMaterial{Version: 1, SubmissionID: "stg_other"})
	intent.SubmissionID = "stg_other"
	w.staged(0)
	w.h.staging.EXPECT().Forget("stg_1").Once()
	saved := w.expectReleased()

	w.run(t)

	require.Equal(t, "submission_in_flight", *(*saved).LastErrorCode)
	require.Equal(t, models.KycIntentUnknown, w.store.state(), "the other submission's intent is left alone")
	w.creator.AssertNotCalled(t, "PrepareCustomer",
		testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
	w.creator.AssertNotCalled(t, "CreatePreparedCustomer", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// Past the provider's idempotency window a replay could create a second
// customer: the intent goes to an operator instead.
func TestAtomicPastRetryDeadlineGoesToManualReview(t *testing.T) {
	w := newAtomicWorker(t, "redis")
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
	require.Equal(t, "safe_replay_window_expired", w.store.failureCode())
	require.NotNil(t, *saved)
	w.creator.AssertNotCalled(t, "CreatePreparedCustomer", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// A scrubbed intent reads like an empty one; nothing is prepared or sent from it.
func TestAtomicScrubbedMaterialGoesToManualReview(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	intent := w.store.seed(models.KycIntentUnknown, repositories.KycIntentMaterial{})
	cleared := time.Now().Add(-time.Hour)
	intent.MaterialClearedAt = &cleared
	w.staged(0)
	w.h.staging.EXPECT().Forget("stg_1").Once()
	w.expectReleased()

	w.run(t)

	require.Equal(t, models.KycIntentManualReview, w.store.state())
	require.Equal(t, "material_cleared", w.store.failureCode())
	w.creator.AssertNotCalled(t, "PrepareCustomer",
		testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
	w.creator.AssertNotCalled(t, "CreatePreparedCustomer", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// A material that does not open cannot be replayed safely.
func TestAtomicUnreadableIntentGoesToManualReview(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	w.store.seed(models.KycIntentUnknown, repositories.KycIntentMaterial{})
	w.store.findErr = repositories.ErrKycIntentIntegrity
	w.staged(0)
	w.h.staging.EXPECT().Forget("stg_1").Once()
	w.expectReleased()

	w.run(t)

	require.Equal(t, models.KycIntentManualReview, w.store.state())
	require.Equal(t, "intent_integrity_failure", w.store.failureCode())
}

// The sync driver has no later: a transient failure is handed to the sweeper
// instead of re-queued into itself.
func TestAtomicInlineQueueDoesNotRecurse(t *testing.T) {
	w := newAtomicWorker(t, "sync")
	w.store.seed(models.KycIntentPrepared, repositories.KycIntentMaterial{
		Version: 1, SubmissionID: "stg_1", ClientIPAddress: atomicClientIP, Prepared: atomicPrepared(),
	})
	w.staged(0)
	w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, ramp.ErrRequestNotSent).Once()
	w.expectReleased()

	before := time.Now()
	w.run(t)

	w.h.dispatcher.AssertNotCalled(t, "DispatchAfter",
		testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
	require.Equal(t, models.KycIntentPrepared, w.store.state(), "nothing left: the outcome is known")
	require.NotNil(t, w.store.intent.RecoveryNextAttemptAt, "scheduled for the sweeper")
	require.WithinRange(t, *w.store.intent.RecoveryNextAttemptAt,
		before.Add(150*time.Second), time.Now().Add(150*time.Second))
}

// A create refused before a connection existed — an unconfigured client, a
// refused dial — never reached the provider. It is rescheduled without being
// marked unknown and without counting as an attempt: the first create that
// does reach the provider is still the first, and its refusal still proves
// nothing was made.
func TestAtomicCreateThatNeverLeftIsNotAnAttempt(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	w.store.seed(models.KycIntentPrepared, repositories.KycIntentMaterial{
		Version: 1, SubmissionID: "stg_1", ClientIPAddress: atomicClientIP, Prepared: atomicPrepared(),
	})
	w.staged(0)
	w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, fmt.Errorf("blindpay: %w: no api key", ramp.ErrRequestNotSent)).Once()
	w.h.staging.EXPECT().Update("stg_1", testifymock.Anything).Return(nil).Once()
	w.h.dispatcher.EXPECT().DispatchAfter("stg_1", uint(7), models.ProviderBridge, time.Second).
		RunAndReturn(func(string, uint, string, time.Duration) error {
			w.journal = append(w.journal, "dispatcher.DispatchAfter")
			return nil
		}).Once()

	w.run(t)

	require.Equal(t, []string{
		"intent.FindActive", "intent.BeginSubmitting", "intent.RevertAttempt", "intent.ScheduleRecovery",
		"dispatcher.DispatchAfter",
	}, w.journal)
	require.Equal(t, models.KycIntentPrepared, w.store.state())
	require.Zero(t, w.store.intent.AttemptCount)
	require.Nil(t, w.store.intent.LastAttemptAt)
	require.Zero(t, w.store.intent.RecoveryAttempts)
	require.NotNil(t, w.store.intent.RecoveryNextAttemptAt)

	// The next run reaches BlindPay, which refuses the person: that is the
	// first attempt, so the intent ends and the row is released with it.
	w.journal = nil
	w.staged(1)
	w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, fmt.Errorf("blindpay: 400: %w", ramp.ErrKycDataInvalid)).Once()
	w.h.staging.EXPECT().Forget("stg_1").Once()
	saved := w.expectReleased()

	w.run(t)

	require.Equal(t, 1, w.store.intent.AttemptCount)
	require.Equal(t, models.KycIntentFailed, w.store.state())
	require.Equal(t, "kyc_data_invalid", *(*saved).LastErrorCode)
}

// The window ran out before a single create left: no customer can exist, so
// the intent fails and the row is let go with a failure the user can retry
// from — not held for an operator.
func TestAtomicPastRetryDeadlineNeverSentFails(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	intent := w.store.seed(models.KycIntentPrepared, repositories.KycIntentMaterial{
		Version: 1, SubmissionID: "stg_1", Prepared: atomicPrepared(),
	})
	intent.RetryDeadline = time.Now().Add(-time.Minute)
	w.staged(0)
	w.h.staging.EXPECT().Forget("stg_1").Once()
	saved := w.expectReleased()

	w.run(t)

	require.Equal(t, models.KycIntentFailed, w.store.state())
	require.Equal(t, repositories.KycIntentCodeNeverSent, w.store.failureCode())
	require.Equal(t, ramp.DefaultErrorCode, *(*saved).LastErrorCode)
	w.creator.AssertNotCalled(t, "CreatePreparedCustomer", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// The images are gone but the body was prepared before they went: the create
// is sent from the intent alone.
func TestAtomicExpiredStagingResumesFromThePreparedBody(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	intent := w.store.seed(models.KycIntentUnknown, repositories.KycIntentMaterial{
		Version: 1, SubmissionID: "stg_1", ClientIPAddress: atomicClientIP, Prepared: atomicPrepared(),
	})
	w.h.staging.EXPECT().Get("stg_1").Return(nil, kyc.ErrStagingExpired).Once()
	w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, intent.ProviderIdempotencyKey, *atomicPrepared()).
		Return(&ramp.KycState{ExternalCustomerID: "cus_bp", Status: models.KycStatusUnderReview}, nil).Once()
	w.expectLinked()
	w.h.staging.EXPECT().Forget("stg_1").Maybe()
	w.h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	w.run(t)

	require.Equal(t, models.KycIntentCompleted, w.store.state())
	w.creator.AssertNotCalled(t, "PrepareCustomer",
		testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// The images went before the body was prepared: there is nothing left to
// send, so the intent fails and the row is released.
func TestAtomicExpiredStagingBeforeThePrepareFails(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	w.store.seed(models.KycIntentPreparing, repositories.KycIntentMaterial{Version: 1, SubmissionID: "stg_1"})
	w.h.staging.EXPECT().Get("stg_1").Return(nil, kyc.ErrStagingExpired).Once()
	saved := w.expectReleased()

	w.run(t)

	require.Equal(t, models.KycIntentFailed, w.store.state())
	require.Equal(t, "submission_expired", w.store.failureCode())
	require.Equal(t, "submission_expired", *(*saved).LastErrorCode)
}

// A failed create frees the person's one slot, and the next submission would
// make a second customer under a new key. So the intent fails only on a
// refusal that proves nothing was made — a refusal of the data, on the very
// first attempt. Anything else goes to an operator.
func TestAtomicCreateFailureThatProvesNothingGoesToManualReview(t *testing.T) {
	for name, tc := range map[string]struct {
		attemptsBefore int
		err            error
	}{
		"2xx with no usable id": {0, fmt.Errorf("blindpay: customer create answered no usable id: %w", ramp.ErrProviderFailed)},
		"2xx that did not decode": {0, fmt.Errorf("blindpay: POST customers: decode response: %w: %w",
			ramp.ErrProviderFailed, errors.New("unexpected end of JSON input"))},
		"key replayed with another body": {0, fmt.Errorf("blindpay: 400: %w: %w", ramp.ErrIdempotencyConflict, ramp.ErrProviderFailed)},
		"key refused":                    {0, fmt.Errorf("blindpay: 401: %w", ramp.ErrProviderCredentialsInvalid)},
		"material no longer matches":     {0, fmt.Errorf("blindpay: digest: %w", ramp.ErrAtomicMaterialInvalid)},
		"refusal of a replay":            {1, fmt.Errorf("blindpay: 400: %w", ramp.ErrKycDataInvalid)},
	} {
		t.Run(name, func(t *testing.T) {
			w := newAtomicWorker(t, "redis")
			intent := w.store.seed(models.KycIntentUnknown, repositories.KycIntentMaterial{
				Version: 1, SubmissionID: "stg_1", ClientIPAddress: atomicClientIP, Prepared: atomicPrepared(),
			})
			intent.AttemptCount = tc.attemptsBefore
			w.staged(0)
			w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
				Return(nil, tc.err).Once()
			w.h.staging.EXPECT().Forget("stg_1").Once()
			w.expectReleased()

			w.run(t)

			require.Equal(t, models.KycIntentManualReview, w.store.state())
			require.Equal(t, "create_outcome_unknown", w.store.failureCode())
			require.Equal(t, tc.attemptsBefore+1, w.store.intent.AttemptCount)
			w.h.dispatcher.AssertNotCalled(t, "DispatchAfter",
				testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
		})
	}
}

// A checkpoint that does not belong to the run is refused the same way every
// time: the prepare created nothing, so the intent fails instead of being
// retried until the deadline.
func TestAtomicMismatchedMaterialFailsThePrepare(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	w.staged(0)
	w.creator.EXPECT().PrepareCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, fmt.Errorf("blindpay: checkpoint version 2: %w", ramp.ErrAtomicMaterialInvalid)).Once()
	w.h.staging.EXPECT().Forget("stg_1").Once()
	w.expectReleased()

	w.run(t)

	require.Equal(t, models.KycIntentFailed, w.store.state())
	w.h.dispatcher.AssertNotCalled(t, "DispatchAfter",
		testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// A run mid-attempt holds the intent's lease. A second delivery of the job
// leaves the intent, the row and the staging to it.
func TestAtomicLeasedIntentIsLeftToItsHolder(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	intent := w.store.seed(models.KycIntentSubmitting, repositories.KycIntentMaterial{
		Version: 1, SubmissionID: "stg_1", ClientIPAddress: atomicClientIP, Prepared: atomicPrepared(),
	})
	started := time.Now().Add(-time.Minute)
	lease := started.Add(150 * time.Second)
	intent.AttemptCount, intent.LastAttemptAt, intent.RecoveryLeaseExpiresAt = 1, &started, &lease
	revision := intent.Revision
	w.staged(0)

	w.run(t)

	require.Equal(t, revision, w.store.intent.Revision, "the intent is untouched")
	require.Equal(t, []string{"intent.FindActive"}, w.journal)
	w.h.kycRepo.AssertNotCalled(t, "UpdateIfStatus", testifymock.Anything, testifymock.Anything)
	w.h.staging.AssertNotCalled(t, "Forget", "stg_1")
	w.creator.AssertNotCalled(t, "CreatePreparedCustomer", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// The sweeper's lease is the dispatched run's to use: it claimed the intent
// in order to hand it to that run.
func TestAtomicSweeperLeaseIsTheDispatchedRuns(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	intent := w.store.seed(models.KycIntentUnknown, repositories.KycIntentMaterial{
		Version: 1, SubmissionID: "stg_1", ClientIPAddress: atomicClientIP, Prepared: atomicPrepared(),
	})
	attempted := time.Now().Add(-10 * time.Minute)
	intent.AttemptCount, intent.LastAttemptAt = 1, &attempted
	w.store.claim(time.Now().Add(150 * time.Second))
	w.journal = nil
	w.staged(0)
	w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{ExternalCustomerID: "cus_bp"}, nil).Once()
	w.expectLinked()
	w.h.staging.EXPECT().Forget("stg_1").Once()
	w.h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	w.run(t)

	require.Equal(t, models.KycIntentCompleted, w.store.state())
}

// The sweeper claims the intent while this run is preparing it: the run's
// next write is stale, and it stops there — nothing is sent, and the intent,
// the row and the staging are all the claimant's, as for a leased intent.
func TestAtomicYieldsWhenTheIntentIsClaimedMidRun(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	w.staged(0)
	w.h.staging.EXPECT().Update("stg_1", testifymock.Anything).Return(nil).Maybe()
	w.creator.EXPECT().PrepareCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(_ context.Context, sub ramp.AtomicKycSubmission, _ ramp.AtomicKycCheckpoint,
			persist func(ramp.AtomicKycCheckpoint) error,
		) (*ramp.PreparedAtomicKyc, error) {
			w.store.claim(time.Now().Add(150 * time.Second))
			err := persist(ramp.AtomicKycCheckpoint{Version: 1, SubmissionID: sub.SubmissionID})
			require.ErrorIs(t, err, repositories.ErrStaleKycIntent)
			return nil, fmt.Errorf("blindpay: persist upload checkpoint: %w", err)
		}).Once()

	w.run(t)

	require.Equal(t, []string{
		"intent.FindActive", "intent.FindBySubmission", "intent.Create", "sweeper.Claim", "intent.Checkpoint (stale)",
	}, w.journal)
	require.Equal(t, models.KycIntentPreparing, w.store.state(), "the claimant's to settle")
	require.Equal(t, 1, w.store.intent.RecoveryAttempts)
	w.h.kycRepo.AssertNotCalled(t, "UpdateIfStatus", testifymock.Anything, testifymock.Anything)
	w.creator.AssertNotCalled(t, "CreatePreparedCustomer", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	w.h.staging.AssertNotCalled(t, "Forget", "stg_1")
}

// BlindPay answering that the terms were already accepted is a customer that
// may exist — an orphan of an earlier create, say. Even on the first attempt
// the intent is held, never failed: failing it would free the slot for a
// second customer.
func TestAtomicAlreadyAcceptedOnTheFirstAttemptGoesToManualReview(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	w.store.seed(models.KycIntentPrepared, repositories.KycIntentMaterial{
		Version: 1, SubmissionID: "stg_1", ClientIPAddress: atomicClientIP, Prepared: atomicPrepared(),
	})
	w.staged(0)
	w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(nil, &blindpay.UpstreamError{
			Method: "POST", Path: "customers", Status: 400, Code: "TOS_ALREADY_ACCEPTED",
		}).Once()
	w.h.staging.EXPECT().Forget("stg_1").Once()
	w.expectReleased()

	w.run(t)

	require.Equal(t, 1, w.store.intent.AttemptCount)
	require.Equal(t, models.KycIntentManualReview, w.store.state())
	require.Equal(t, "create_outcome_unknown", w.store.failureCode())
}

// releasedRow is the row after the worker let it go and handed the intent to
// the sweeper: off `submitting`, the application still held, the failure the
// user was shown recorded.
func releasedRow() *models.CustomerProviderKyc {
	row := deferredRow()
	row.KycStatus = models.KycStatusIncomplete
	row.RecordSubmissionFailure("provider_unavailable", "blindpay: request not sent")
	return row
}

// A replay that creates the customer after the row was released finds the row
// no longer `submitting`, so the guarded write misses. The row still holds the
// application the create was for, so the customer is linked onto the status
// it holds now, the held application and the failure cleared, and published.
func TestAtomicReplayLinksTheCustomerOnAReleasedRow(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	w.row = releasedRow()
	intent := w.store.seed(models.KycIntentUnknown, repositories.KycIntentMaterial{
		Version: 1, SubmissionID: "stg_1", ClientIPAddress: atomicClientIP, Prepared: atomicPrepared(),
	})
	intent.AttemptCount = 1
	w.store.claim(time.Now().Add(150 * time.Second))
	w.h.staging.EXPECT().Get("stg_1").Return(nil, kyc.ErrStagingExpired).Once()
	w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, intent.ProviderIdempotencyKey, *atomicPrepared()).
		Return(&ramp.KycState{ExternalCustomerID: "cus_bp", Status: models.KycStatusUnderReview}, nil).Once()
	w.h.kycRepo.EXPECT().SaveWithLink(testifymock.Anything, false, w.link, models.KycStatusSubmitting).
		Return(false, nil).Once()
	var saved *models.CustomerProviderKyc
	w.h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusIncomplete).
		RunAndReturn(func(row *models.CustomerProviderKyc, _ string) (bool, error) {
			saved = row
			return true, nil
		}).Once()
	w.h.staging.EXPECT().Forget("stg_1").Maybe()
	w.h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.KycStatus == models.KycStatusUnderReview && e.FailureReason == ""
	})).Once()

	w.run(t)

	require.Equal(t, models.KycIntentCompleted, w.store.state())
	require.Equal(t, "cus_bp", *w.link.ExternalCustomerID)
	require.NotNil(t, saved, "the released row is written")
	require.Equal(t, models.KycStatusUnderReview, saved.KycStatus)
	require.Nil(t, saved.ApplicationDeferredAt, "the customer exists: nothing is held any more")
	require.Nil(t, saved.LastErrorCode, "the failure the release recorded is cleared")
	require.Equal(t, []string{models.KycStatusUnderReview}, w.h.verdicts)
}

// The row moved because something that knows the customer wrote it — a
// webhook, a refresh — and no longer holds the application: it is left as it
// is, as before.
func TestAtomicReplayLeavesARowThatNoLongerHoldsTheApplication(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	w.row = kycRow(models.KycStatusApproved)
	w.store.seed(models.KycIntentUnknown, repositories.KycIntentMaterial{
		Version: 1, SubmissionID: "stg_1", ClientIPAddress: atomicClientIP, Prepared: atomicPrepared(),
	})
	w.h.staging.EXPECT().Get("stg_1").Return(nil, kyc.ErrStagingExpired).Once()
	w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{ExternalCustomerID: "cus_bp", Status: models.KycStatusUnderReview}, nil).Once()
	w.h.kycRepo.EXPECT().SaveWithLink(testifymock.Anything, false, w.link, models.KycStatusSubmitting).
		Return(false, nil).Once()
	w.h.staging.EXPECT().Forget("stg_1").Maybe()

	w.run(t)

	require.Equal(t, models.KycIntentCompleted, w.store.state())
	w.h.kycRepo.AssertNotCalled(t, "UpdateIfStatus", testifymock.Anything, testifymock.Anything)
	w.h.broadcaster.AssertNotCalled(t, "PublishKyc", testifymock.Anything, testifymock.Anything)
}

// The link already names another customer: the one this create made is not
// written over it. The intent goes to an operator, who decides between them.
func TestAtomicCustomerConflictGoesToManualReview(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	other := "cus_other"
	w.link.ExternalCustomerID = &other
	w.store.seed(models.KycIntentPrepared, repositories.KycIntentMaterial{
		Version: 1, SubmissionID: "stg_1", ClientIPAddress: atomicClientIP, Prepared: atomicPrepared(),
	})
	w.staged(0)
	w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{ExternalCustomerID: "cus_bp", Status: models.KycStatusUnderReview}, nil).Once()
	w.h.staging.EXPECT().Forget("stg_1").Once()
	saved := w.expectReleased()

	w.run(t)

	require.Equal(t, models.KycIntentManualReview, w.store.state())
	require.Equal(t, "customer_conflict", w.store.failureCode())
	require.Equal(t, "cus_bp", *w.store.intent.ProviderCustomerID, "the operator sees both customers")
	require.Equal(t, "cus_other", *w.link.ExternalCustomerID, "the linked customer is not overwritten")
	require.Equal(t, "submission_held", *(*saved).LastErrorCode)
	w.h.kycRepo.AssertNotCalled(t, "SaveWithLink", testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// The conflict found on a released row: nothing is written on the row either.
func TestAtomicCustomerConflictOnAReleasedRowLeavesTheRow(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	w.row = releasedRow()
	other := "cus_other"
	w.link.ExternalCustomerID = &other
	w.store.seed(models.KycIntentUnknown, repositories.KycIntentMaterial{
		Version: 1, SubmissionID: "stg_1", ClientIPAddress: atomicClientIP, Prepared: atomicPrepared(),
	})
	w.h.staging.EXPECT().Get("stg_1").Return(nil, kyc.ErrStagingExpired).Once()
	w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{ExternalCustomerID: "cus_bp"}, nil).Once()
	w.h.staging.EXPECT().Forget("stg_1").Maybe()

	w.run(t)

	require.Equal(t, models.KycIntentManualReview, w.store.state())
	require.Equal(t, "customer_conflict", w.store.failureCode())
	w.h.kycRepo.AssertNotCalled(t, "SaveWithLink", testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
	w.h.kycRepo.AssertNotCalled(t, "UpdateIfStatus", testifymock.Anything, testifymock.Anything)
}

// Documents sent while an operator holds the person's intent are told so,
// not that a submission is in flight: nothing is, and none will finish.
func TestAtomicDocumentsWhileAnIntentIsHeldAreRefusedAsHeld(t *testing.T) {
	store := &intentStore{journal: new([]string)}
	store.seed(models.KycIntentManualReview, repositories.KycIntentMaterial{Version: 1, SubmissionID: "stg_1"})
	h, creator := newAtomicHarnessWith(t, "redis", store)
	creator.EXPECT().ValidateAtomicDocuments(testifymock.Anything).Return(nil).Once()
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	row := deferredRow()
	row.KycStatus = models.KycStatusIncomplete
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()

	_, err := h.svc.SubmitDocuments(context.Background(), h.user, 7, models.ProviderBridge, atomicDocumentsDTO())
	require.ErrorIs(t, err, kyc.ErrSubmissionHeld)
	h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
}

// The link gained another customer between the worker's read and its write:
// the write refuses (ErrLinkConflict), and the intent goes to an operator.
func TestAtomicLinkRaceGoesToManualReview(t *testing.T) {
	w := newAtomicWorker(t, "redis")
	w.store.seed(models.KycIntentPrepared, repositories.KycIntentMaterial{
		Version: 1, SubmissionID: "stg_1", ClientIPAddress: atomicClientIP, Prepared: atomicPrepared(),
	})
	w.staged(0)
	w.creator.EXPECT().CreatePreparedCustomer(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{ExternalCustomerID: "cus_bp", Status: models.KycStatusUnderReview}, nil).Once()
	w.h.kycRepo.EXPECT().SaveWithLink(testifymock.Anything, false, w.link, models.KycStatusSubmitting).
		Return(false, fmt.Errorf("user provider kyc repo: save with link: %w", repositories.ErrLinkConflict)).Once()
	w.h.staging.EXPECT().Forget("stg_1").Once()
	saved := w.expectReleased()

	w.run(t)

	require.Equal(t, models.KycIntentManualReview, w.store.state())
	require.Equal(t, "customer_conflict", w.store.failureCode())
	require.Nil(t, w.link.ExternalCustomerID, "the refused write leaves the link as it was read")
	require.Equal(t, models.KycStatusIncomplete, (*saved).KycStatus)
	require.Equal(t, "submission_held", *(*saved).LastErrorCode)
}

// An atomic create staged for a provider that does not create customers
// atomically is a wiring fault, not a hold: the row is released with the
// default code.
func TestAtomicCreateAtANonAtomicProviderIsReleasedAsAFault(t *testing.T) {
	h := newHarnessWithQueue(t, "redis")
	docs := atomicDocuments()
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindAtomicCustomer,
		PreviousStatus: models.KycStatusIncomplete, StagedAt: time.Now(), Documents: &docs,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(deferredRow(), nil).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	var saved *models.CustomerProviderKyc
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusSubmitting).
		RunAndReturn(func(row *models.CustomerProviderKyc, _ string) (bool, error) { saved = row; return true, nil }).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", 7, models.ProviderBridge))

	require.Equal(t, ramp.DefaultErrorCode, *saved.LastErrorCode)
}
