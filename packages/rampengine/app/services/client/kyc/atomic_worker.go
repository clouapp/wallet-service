package kyc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
)

// The bounds of an atomic customer create.
const (
	// atomicRetryWindow is how long an intent may be replayed: the provider
	// keeps an idempotency key for 24 hours, and a replay past that could
	// create a second customer instead of answering with the first.
	atomicRetryWindow = 24 * time.Hour
	// atomicLease is how long one attempt holds the intent. It outlasts
	// workerTimeout, so a worker still running is never swept from under
	// itself, and a worker that died hands the intent to the sweeper when it
	// runs out.
	atomicLease = 2*time.Minute + 30*time.Second
	// atomicMaxDispatches is how often the worker re-queues a create itself
	// before leaving it to the sweeper, which counts its own attempts.
	atomicMaxDispatches = 8
	// atomicMaxBackoffShift caps the self-dispatch delay at 64 seconds.
	atomicMaxBackoffShift = 6
	// atomicMaterialVersion is the version of the intent material written here.
	atomicMaterialVersion = 1
)

// Failure codes this worker writes on an intent. The two the sweeper shares
// are the repository's.
const (
	intentCodeOutcomeUnknown  = "create_outcome_unknown"
	intentCodePrepareRetry    = "prepare_retry"
	intentCodeMaterialCleared = "material_cleared"
	intentCodeLinkFailed      = "link_persist_failed"
	// intentCodeNotSent schedules again a create refused before it left.
	intentCodeNotSent = "create_not_sent"
	// intentCodeCustomerConflict holds an intent whose create made a customer
	// while the link already names another one.
	intentCodeCustomerConflict = "customer_conflict"
)

// errIntentNeverSent is the failure a row is let go with when its intent ran
// out of attempts or of its window without one create ever sent. It reads as
// the provider being unavailable — which, from here, it was — and leaves the
// documents to send again; ErrRequestNotSent says nothing reached the
// provider.
var errIntentNeverSent = fmt.Errorf("kyc: the customer create was never sent: %w", ramp.ErrRequestNotSent)

// atomicRun is one worker pass over an atomic create: what it read, so each
// step does not take nine arguments.
type atomicRun struct {
	submissionID string
	submission   *StagedSubmission
	row          *models.CustomerProviderKyc
	isNew        bool
	link         *models.CustomerProviderIntegration
	creator      ramp.AtomicKycCreator
	started      time.Time
	intent       *models.ProviderKycSubmissionIntent
	material     *repositories.KycIntentMaterial
}

// processAtomicCustomer creates the customer of a provider that takes the
// whole person in one call, from a durable intent that a replay sends again
// byte for byte.
//
// Every write on the row lands only while it is still `submitting`: the
// worker holds no lock, and anything that moved the row since is newer.
func (s *ProviderKycService) processAtomicCustomer(ctx context.Context, run *atomicRun) error {
	if s.intents == nil {
		facades.Log().Errorf("kyc: submission %s: no intent repository for %s", run.submissionID, run.row.Provider)
		s.staging.Forget(run.submissionID)
		s.release(run.row, run.isNew, run.submission.PreviousStatus, run.row.Provider, run.started, ErrSubmissionHeld)
		return nil
	}
	if !s.openIntent(run) {
		return nil
	}
	if run.intent.MaterialClearedAt != nil {
		s.holdForReview(run, intentCodeMaterialCleared)
		return nil
	}
	if run.intent.State == models.KycIntentManualReview {
		s.staging.Forget(run.submissionID)
		s.release(run.row, run.isNew, run.submission.PreviousStatus, run.row.Provider, run.started, ErrSubmissionHeld)
		return nil
	}
	if time.Now().After(run.intent.RetryDeadline) {
		if run.intent.AttemptCount == 0 {
			// Nothing ever left: no customer exists to decide about.
			s.endNeverSent(run)
			return nil
		}
		s.holdForReview(run, repositories.KycIntentCodeReplayWindowEnded)
		return nil
	}
	if run.material.Prepared == nil && !s.prepareAtomic(ctx, run) {
		return nil
	}
	s.createAtomic(ctx, run)
	return nil
}

// openIntent finds this submission's intent, or creates it, and reports
// whether the run goes on.
func (s *ProviderKycService) openIntent(run *atomicRun) bool {
	customerID, provider := run.row.CustomerID, run.row.Provider
	intent, material, err := s.intents.FindActive(customerID, provider)
	switch {
	case errors.Is(err, repositories.ErrKycIntentIntegrity) && intent != nil:
		run.intent = intent
		s.holdForReview(run, repositories.KycIntentCodeIntegrityFailure)
		return false
	case err != nil:
		facades.Log().Errorf("kyc: submission %s: read the active intent: %v", run.submissionID, err)
		s.staging.Forget(run.submissionID)
		s.release(run.row, run.isNew, run.submission.PreviousStatus, provider, run.started, err)
		return false
	case intent != nil && intent.SubmissionID != run.submissionID:
		// Another submission owns the one create this person may have —
		// still running, or held for an operator.
		failure := ErrSubmissionInFlight
		if intent.State == models.KycIntentManualReview {
			failure = ErrSubmissionHeld
		}
		s.staging.Forget(run.submissionID)
		s.release(run.row, run.isNew, run.submission.PreviousStatus, provider, run.started, failure)
		return false
	case intent != nil && leasedToAnotherRun(intent, time.Now()):
		// Another run is mid-attempt on this intent. It owns the row and the
		// staging too, and settles both; this one leaves everything as it is.
		facades.Log().Warningf("kyc: submission %s: intent %s is leased to another run until %s",
			run.submissionID, intent.ExternalID, intent.RecoveryLeaseExpiresAt.Format(time.RFC3339))
		return false
	case intent != nil:
		run.intent, run.material = intent, material
		return true
	}
	if run.submission.Documents == nil {
		s.staging.Forget(run.submissionID)
		s.release(run.row, run.isNew, run.submission.PreviousStatus, provider, run.started, ErrStagingExpired)
		return false
	}
	if s.alreadyCreated(run) {
		return false
	}
	now := time.Now()
	external := uuid.NewString()
	lease := now.Add(atomicLease)
	intent = &models.ProviderKycSubmissionIntent{
		ExternalID: external, CustomerID: customerID, Provider: provider, SubmissionID: run.submissionID,
		ProviderIdempotencyKey: repositories.KycIntentKeyPrefix + external,
		RetryDeadline:          now.Add(atomicRetryWindow),
		// Leased from birth: a worker that dies before its first send leaves
		// the intent to the sweeper rather than nowhere.
		RecoveryLeaseExpiresAt: &lease,
	}
	// The checkpoint is the zero value: the one a first prepare accepts.
	material = &repositories.KycIntentMaterial{
		Version: atomicMaterialVersion, SubmissionID: run.submissionID,
		ClientIPAddress: run.submission.Documents.ClientIPAddress,
	}
	if err := s.intents.Create(intent, *material); err != nil {
		if errors.Is(err, repositories.ErrActiveKycIntentExists) {
			err = ErrSubmissionInFlight
		}
		facades.Log().Errorf("kyc: submission %s: create the intent: %v", run.submissionID, err)
		s.staging.Forget(run.submissionID)
		s.release(run.row, run.isNew, run.submission.PreviousStatus, provider, run.started, err)
		return false
	}
	run.intent, run.material = intent, material
	return true
}

// alreadyCreated reports whether this submission must not open an intent
// because it, or the person, already has its customer: a stale delivery of a
// create that finished — the job delivered twice, its staging still there —
// finds no active intent, and opening one would send a new key, which the
// provider answers with a second customer.
//
// Two reads decide it, made now rather than trusted from the run's start: the
// link naming a customer, and an intent this submission opened, in any state.
// Either way the staging is dropped and the row is left as it is: whatever
// ended that create settled it, and a row `submitting` now may be another
// submission's.
func (s *ProviderKycService) alreadyCreated(run *atomicRun) bool {
	customerID, provider := run.row.CustomerID, run.row.Provider
	link, err := s.integRepo.FindByCustomerAndProvider(customerID, provider)
	if err != nil {
		facades.Log().Errorf("kyc: submission %s: read the link again: %v", run.submissionID, err)
		s.staging.Forget(run.submissionID)
		s.release(run.row, run.isNew, run.submission.PreviousStatus, provider, run.started, err)
		return true
	}
	if hasExternalCustomer(link) {
		facades.Log().Warningf("kyc: submission %s: %s already links customer %s; no create is opened",
			run.submissionID, provider, *link.ExternalCustomerID)
		s.staging.Forget(run.submissionID)
		return true
	}
	ended, err := s.intents.FindBySubmission(customerID, provider, run.submissionID)
	if err != nil {
		facades.Log().Errorf("kyc: submission %s: read its intent: %v", run.submissionID, err)
		s.staging.Forget(run.submissionID)
		s.release(run.row, run.isNew, run.submission.PreviousStatus, provider, run.started, err)
		return true
	}
	if ended != nil {
		facades.Log().Warningf("kyc: submission %s: its intent %s already ended (%s); no second create is opened",
			run.submissionID, ended.ExternalID, ended.State)
		s.staging.Forget(run.submissionID)
		return true
	}
	return false
}

// leasedToAnotherRun reports whether a worker attempt holds the intent's lease
// right now, so a second run must not touch it.
//
// The lease has two writers. A worker leases the intent for its attempt:
// Create leases a fresh intent (no attempt yet, never claimed), and
// BeginSubmitting leases it from the attempt's start (lease = last attempt +
// atomicLease, written together). The sweeper leases what it claims and then
// dispatches the very run that should take it, so its lease is the dispatched
// run's to use: it leaves LastAttemptAt alone and counts a recovery attempt,
// which is what tells the two apart.
func leasedToAnotherRun(intent *models.ProviderKycSubmissionIntent, now time.Time) bool {
	lease := intent.RecoveryLeaseExpiresAt
	if lease == nil || !lease.After(now) {
		return false
	}
	if intent.LastAttemptAt == nil {
		return intent.RecoveryAttempts == 0
	}
	return lease.Equal(intent.LastAttemptAt.Add(atomicLease))
}

// prepareAtomic uploads the files and builds the exact create body, checkpointing
// every upload into the intent, and reports whether the run goes on to send it.
func (s *ProviderKycService) prepareAtomic(ctx context.Context, run *atomicRun) bool {
	provider := run.row.Provider
	if run.submission.Documents == nil {
		// Only a submission rebuilt from its intent has no documents, and it
		// is rebuilt only once the body is prepared.
		s.failAtomic(run, ErrStagingExpired)
		return false
	}
	app, err := s.deferredApplication(run.row)
	if err != nil {
		s.failAtomic(run, err)
		return false
	}
	docs := *run.submission.Documents
	if docs.ClientIPAddress == "" {
		docs.ClientIPAddress = run.material.ClientIPAddress
	}
	submission := ramp.AtomicKycSubmission{
		SubmissionID: run.submissionID, Reference: run.intent.ExternalID,
		Application: app, Documents: docs,
	}
	persist := func(cp ramp.AtomicKycCheckpoint) error {
		run.material.Checkpoint = cp
		if err := s.intents.Checkpoint(run.intent, *run.material); err != nil {
			return err
		}
		// The intent is the durable copy; the staging one travels with the
		// images it was uploaded from, and losing it costs nothing.
		run.submission.AtomicCheckpoint = &cp
		if !run.submission.resumed {
			if err := s.staging.Update(run.submissionID, *run.submission); err != nil {
				facades.Log().Warningf("kyc: submission %s: stage the upload checkpoint: %v", run.submissionID, err)
			}
		}
		return nil
	}
	sent := time.Now()
	prepared, err := run.creator.PrepareCustomer(ctx, submission, run.material.Checkpoint, persist)
	s.record(rampsvc.Entry{
		UserID: run.submission.UserID, CustomerID: run.row.CustomerID, Provider: provider, Operation: rampsvc.OpSubmitDocuments,
		SubmissionID: run.submissionID, Started: sent, Err: err,
	})
	switch {
	case errors.Is(err, repositories.ErrStaleKycIntent):
		s.yieldIntent(run, err)
		return false
	case err != nil && isTransient(err):
		s.rescheduleAtomic(run, intentCodePrepareRetry, err)
		return false
	case err != nil:
		// Nothing is created by a prepare — a mismatched checkpoint
		// (ErrAtomicMaterialInvalid) included — so the intent can end.
		s.failAtomic(run, err)
		return false
	}
	run.material.Prepared = prepared
	if err := s.intents.MarkPrepared(run.intent, *run.material); err != nil {
		if errors.Is(err, repositories.ErrStaleKycIntent) {
			s.yieldIntent(run, err)
			return false
		}
		facades.Log().Errorf("kyc: submission %s: mark the intent prepared: %v", run.submissionID, err)
		run.material.Prepared = nil
		s.rescheduleAtomic(run, intentCodePrepareRetry, err)
		return false
	}
	return true
}

// deferredApplication rebuilds the application deferApplication held, from
// the profile it retained and the agreement on the row.
func (s *ProviderKycService) deferredApplication(row *models.CustomerProviderKyc) (ramp.Application, error) {
	profile, err := s.profileRepo.Find(row.CustomerID)
	if err != nil {
		return ramp.Application{}, fmt.Errorf("kyc: read the retained profile: %w", err)
	}
	if profile == nil {
		return ramp.Application{}, fmt.Errorf("kyc: no retained profile to create the customer from: %w", ErrApplicationRequired)
	}
	app := ramp.Application{Type: "individual", Profile: *profile}
	if row.SignedAgreementID != nil {
		app.SignedAgreementID = *row.SignedAgreementID
	}
	if profile.TaxID != "" {
		app.IdentifyingInformation = []ramp.IdentifyingDocument{taxIDDocument(profile.TaxID, profile.TaxIdentity())}
	}
	return app, nil
}

// createAtomic sends the prepared body under the intent's key and settles
// the outcome.
func (s *ProviderKycService) createAtomic(ctx context.Context, run *atomicRun) {
	provider := run.row.Provider
	now := time.Now()
	before := *run.intent
	if err := s.intents.BeginSubmitting(run.intent, now, now.Add(atomicLease)); err != nil {
		if errors.Is(err, repositories.ErrStaleKycIntent) {
			s.yieldIntent(run, err)
			return
		}
		facades.Log().Errorf("kyc: submission %s: begin the create: %v", run.submissionID, err)
		s.rescheduleAtomic(run, intentCodeOutcomeUnknown, err)
		return
	}
	stored := *run.row
	sent := time.Now()
	state, err := run.creator.CreatePreparedCustomer(ctx, run.intent.ProviderIdempotencyKey, *run.material.Prepared)
	s.record(rampsvc.Entry{
		UserID: run.submission.UserID, CustomerID: run.row.CustomerID, Provider: provider, Operation: rampsvc.OpSubmitDocuments,
		SubmissionID: run.submissionID, Started: sent, Err: err,
	})

	if state != nil && state.ExternalCustomerID != "" {
		// The customer exists, whether or not reading it back worked: it is
		// linked now, and a replay would only find it again.
		if err != nil {
			facades.Log().Warningf("kyc: submission %s: %s created customer %s but reading it failed: %v",
				run.submissionID, provider, state.ExternalCustomerID, err)
		}
		s.linkAtomicCustomer(run, state, &stored, err == nil)
		return
	}
	if err != nil && errors.Is(err, ramp.ErrRequestNotSent) {
		// Refused before a connection existed: the provider never saw it, so
		// the outcome is known — nothing — and the attempt is taken back.
		// The next create that does reach the provider is still the first,
		// and its refusal still proves nothing was made (createdNothing).
		if revertErr := s.intents.RevertAttempt(run.intent, before); revertErr != nil {
			if errors.Is(revertErr, repositories.ErrStaleKycIntent) {
				s.yieldIntent(run, revertErr)
				return
			}
			facades.Log().Errorf("kyc: submission %s: take back the attempt: %v", run.submissionID, revertErr)
		}
		s.rescheduleAtomic(run, intentCodeNotSent, err)
		return
	}
	if err != nil && (isTransient(err) || noAnswer(err)) {
		if markErr := s.intents.MarkUnknown(run.intent, intentCodeOutcomeUnknown); markErr != nil {
			facades.Log().Errorf("kyc: submission %s: mark the intent unknown: %v", run.submissionID, markErr)
		}
		s.rescheduleAtomic(run, intentCodeOutcomeUnknown, err)
		return
	}
	if err == nil {
		err = errors.New("kyc: the provider answered the create without a customer")
	}
	facades.Log().Errorf("kyc: submission %s failed for customer %d at %s: %v", run.submissionID, run.row.CustomerID, provider, err)
	if !createdNothing(run.intent, err) {
		// The customer may exist. Failing the intent would free the slot,
		// and the next submission would create a second one under a new
		// key; an operator decides it instead.
		s.holdForReview(run, intentCodeOutcomeUnknown)
		return
	}
	if markErr := s.intents.MarkFailed(run.intent, failureCode(err), time.Now()); markErr != nil {
		facades.Log().Errorf("kyc: submission %s: mark the intent failed: %v", run.submissionID, markErr)
	}
	if errors.Is(err, ramp.ErrKycDataInvalid) {
		// The create carried the held application as much as the documents,
		// and the provider says nothing of which it refused: the application
		// is let go with the release (in its guarded write), so the row owes
		// it again (markApplication) rather than only the documents.
		stored.ApplicationDeferredAt = nil
	}
	s.staging.Forget(run.submissionID)
	moved := s.releaseInFlight(run.submissionID, run.row, &stored, run.submission.PreviousStatus, run.isNew, err)
	ramp.LogKycSubmission(provider, submitFailedReason, run.started)
	if !moved {
		s.publish(run.row, submitFailedReason)
		s.announceRefusal(stored.KycStatus, run.row, err)
	}
}

// noAnswer reports whether a create reached the provider and no answer came
// back — a timeout, the worker's deadline, a connection that broke under it.
// The customer may exist, and only a replay under the same key can say: such
// a create is sent again, never held. A 2xx that came back unusable is an
// answer, and is not this.
func noAnswer(err error) bool {
	return errors.Is(err, ramp.ErrOutcomeUnknown) || errors.Is(err, context.DeadlineExceeded)
}

// createdNothing reports whether a create's failure proves no customer exists,
// so the intent may end and free the person's slot.
//
// Only a refusal of what was sent proves it — the provider looking at the
// person and creating no one — and only on the first attempt: after an attempt whose
// outcome was unknown, a refusal of the replay says nothing about the first.
// Everything else proves nothing: a 2xx without a usable id or a body that
// did not decode (ErrProviderFailed), a key replayed with another body
// (ErrIdempotencyConflict), the provider refusing the API key, or material
// that no longer matches its intent (ErrAtomicMaterialInvalid).
func createdNothing(intent *models.ProviderKycSubmissionIntent, err error) bool {
	if intent.AttemptCount != 1 {
		return false
	}
	if errors.Is(err, ramp.ErrIdempotencyConflict) || errors.Is(err, ramp.ErrProviderCredentialsInvalid) ||
		errors.Is(err, ramp.ErrAtomicMaterialInvalid) {
		return false
	}
	var refusal *ramp.KycRefusal
	return errors.Is(err, ramp.ErrKycDataInvalid) || errors.As(err, &refusal) || isTosRefusal(err)
}

// yieldIntent stops a run whose intent was written by someone else since it
// was read — the sweeper claimed it, or an operator took it. Nothing more is
// sent or written: the intent, the staging and the row are whoever holds the
// intent now to settle, as for an intent leased to another run (openIntent).
// Releasing the row here would tell the user the submission failed while the
// claimant is about to send it.
func (s *ProviderKycService) yieldIntent(run *atomicRun, err error) {
	facades.Log().Warningf("kyc: submission %s: intent %s changed under the run; yielding: %v",
		run.submissionID, run.intent.ExternalID, err)
}

// linkAtomicCustomer records the customer a create made on the intent, the
// link and the row, then completes the intent. announce is false when reading
// the customer back failed: the row is published, and the devices learn the
// verdict from the refresh that follows.
func (s *ProviderKycService) linkAtomicCustomer(
	run *atomicRun, state *ramp.KycState, stored *models.CustomerProviderKyc, announce bool,
) {
	provider, customerID := run.row.Provider, state.ExternalCustomerID
	if err := s.intents.RecordCustomer(run.intent, customerID); err != nil {
		facades.Log().Errorf("kyc: submission %s: record customer %s on the intent: %v",
			run.submissionID, customerID, err)
	}
	if s.linksAnotherCustomer(run, customerID) {
		// Two customers for one person: neither is written over the other.
		// The intent names this one and the link the other, and an operator
		// decides between them.
		*run.row = *stored
		s.holdForReview(run, intentCodeCustomerConflict)
		return
	}
	if state.Status == "" {
		state.Status = models.KycStatusUnderReview
	}
	applyKycState(run.row, state)
	run.row.ApplicationDeferredAt = nil
	written, err := s.linkAndSave(run.link, customerID, run.row, run.isNew, models.KycStatusSubmitting)
	if errors.Is(err, repositories.ErrLinkConflict) {
		// Another customer was linked between the read above and this write
		// (an operator's reconciliation, say): the write refused to replace
		// it, and the operator decides between the two.
		*run.row = *stored
		s.holdForReview(run, intentCodeCustomerConflict)
		return
	}
	if err != nil {
		// The customer exists upstream and the intent names it, so a replay
		// with the same key finds it again and retries this write.
		facades.Log().Errorf("kyc: submission %s: persist customer %s: %v", run.submissionID, customerID, err)
		*run.row = *stored
		s.rescheduleAtomic(run, intentCodeLinkFailed, err)
		return
	}
	if err := s.intents.Complete(run.intent, time.Now()); err != nil {
		facades.Log().Errorf("kyc: submission %s: complete the intent: %v", run.submissionID, err)
	}
	s.staging.Forget(run.submissionID)
	ramp.LogKycSubmission(provider, run.row.KycStatus, run.started)
	if !written {
		s.linkReleasedRow(run, state, announce)
		return
	}
	s.publish(run.row, "")
	if announce {
		s.notifier.KycUpdated(stored.KycStatus, run.row)
	}
}

// linksAnotherCustomer reports whether the person's link, read now, names a
// customer other than customerID. It is read again rather than trusted from
// the run's start: an operator's reconciliation, or an earlier run, can have
// linked one since. The read only spares the write; the write itself is what
// refuses a race (SaveWithLink answers repositories.ErrLinkConflict).
func (s *ProviderKycService) linksAnotherCustomer(run *atomicRun, customerID string) bool {
	current, err := s.integRepo.FindByCustomerAndProvider(run.row.CustomerID, run.row.Provider)
	if err != nil || current == nil {
		// Nothing better to decide from than the run's own copy: linkAndSave
		// answers a link that is gone by itself.
		current = run.link
	} else {
		run.link = current
	}
	return current.ExternalCustomerID != nil && *current.ExternalCustomerID != "" &&
		*current.ExternalCustomerID != customerID
}

// linkReleasedRow finishes a create whose row was no longer `submitting` when
// its answer came: the run that sent it first had released the row and handed
// the intent to the sweeper, and this replay is what found the customer.
//
// The link is written already. A row that still holds the application the
// create was for (ApplicationDeferredAt) is the released one, waiting for this
// customer: the answer is written onto the status it holds now, the held
// application and the failure the release recorded cleared. A row that no
// longer holds it was written by something that knows the customer — a
// webhook, a refresh — and keeps what it says.
func (s *ProviderKycService) linkReleasedRow(run *atomicRun, state *ramp.KycState, announce bool) {
	provider := run.row.Provider
	current, err := s.kycRepo.FindByCustomerAndProvider(run.row.CustomerID, provider)
	if err != nil || current == nil || current.ApplicationDeferredAt == nil {
		if err != nil {
			facades.Log().Errorf("kyc: submission %s: read the row at %s again: %v", run.submissionID, provider, err)
		}
		facades.Log().Warningf("kyc: submission %s: the row at %s moved during the create; its answer is not written over it",
			run.submissionID, provider)
		return
	}
	previous := current.KycStatus
	applyKycState(current, state)
	current.ApplicationDeferredAt = nil
	current.ClearSubmissionFailure()
	written, err := s.linkAndSave(run.link, state.ExternalCustomerID, current, false, previous)
	if err != nil {
		facades.Log().Errorf("kyc: submission %s: link customer %s onto the released row: %v",
			run.submissionID, state.ExternalCustomerID, err)
		return
	}
	if !written {
		facades.Log().Warningf("kyc: submission %s: the released row at %s moved again; left to the next refresh",
			run.submissionID, provider)
		return
	}
	*run.row = *current
	s.publish(current, "")
	if announce {
		s.notifier.KycUpdated(previous, current)
	}
}

// failAtomic ends an intent that cannot succeed and releases the row with why.
func (s *ProviderKycService) failAtomic(run *atomicRun, failure error) {
	facades.Log().Errorf("kyc: submission %s: prepare at %s failed: %v", run.submissionID, run.row.Provider, failure)
	if err := s.intents.MarkFailed(run.intent, failureCode(failure), time.Now()); err != nil {
		facades.Log().Errorf("kyc: submission %s: mark the intent failed: %v", run.submissionID, err)
	}
	s.staging.Forget(run.submissionID)
	s.release(run.row, run.isNew, run.submission.PreviousStatus, run.row.Provider, run.started, failure)
}

// endNeverSent fails an intent whose replay window closed before one create
// left, and lets the row go with errIntentNeverSent: no customer can exist,
// so there is nothing for an operator to hold it for.
func (s *ProviderKycService) endNeverSent(run *atomicRun) {
	facades.Log().Warningf("kyc: submission %s: intent %s ran out of its window without a create sent",
		run.submissionID, run.intent.ExternalID)
	if err := s.intents.MarkFailed(run.intent, repositories.KycIntentCodeNeverSent, time.Now()); err != nil {
		facades.Log().Errorf("kyc: submission %s: mark the intent failed: %v", run.submissionID, err)
	}
	s.staging.Forget(run.submissionID)
	s.release(run.row, run.isNew, run.submission.PreviousStatus, run.row.Provider, run.started, errIntentNeverSent)
}

// holdForReview takes the intent out of the automation's hands with code and
// releases the row: nothing is prepared or sent from it again.
func (s *ProviderKycService) holdForReview(run *atomicRun, code string) {
	facades.Log().Errorf("kyc: submission %s: intent %s held for review: %s",
		run.submissionID, run.intent.ExternalID, code)
	if err := s.intents.MarkManualReview(run.intent, code, time.Now().Add(s.intentRetention)); err != nil {
		facades.Log().Errorf("kyc: submission %s: hold the intent for review: %v", run.submissionID, err)
	}
	s.staging.Forget(run.submissionID)
	s.release(run.row, run.isNew, run.submission.PreviousStatus, run.row.Provider, run.started, ErrSubmissionHeld)
}

// rescheduleAtomic tries the same create again later, with the same intent.
//
// The worker re-queues itself while it can, backing off from one second to a
// minute: the delay grows with its own count, kept on the staged submission,
// because the intent counts only the sweeper's claims. The next attempt is
// written on the intent before the job is queued, so the sweeper finds it
// even if the queue loses the job.
//
// When it cannot — the sync driver, which would recurse; a submission rebuilt
// from its intent, with no staging to count in; eight re-queues already — the
// intent stays where it is, due for the sweeper one lease from now, and the
// row is released with the failure: the user sees it did not go through, and
// the sweeper's replay links the customer if it did.
func (s *ProviderKycService) rescheduleAtomic(run *atomicRun, code string, failure error) {
	customerID, provider := run.row.CustomerID, run.row.Provider
	now := time.Now()
	if !s.dispatcher.RunsInline() && !run.submission.resumed && run.submission.DispatchAttempts < atomicMaxDispatches {
		delay := time.Second << min(run.submission.DispatchAttempts, atomicMaxBackoffShift)
		next := *run.submission
		next.DispatchAttempts++
		if err := s.staging.Update(run.submissionID, next); err != nil {
			facades.Log().Warningf("kyc: submission %s: count the re-queue: %v", run.submissionID, err)
		} else if err := s.intents.ScheduleRecovery(run.intent, now.Add(delay+atomicLease), code); err != nil {
			facades.Log().Errorf("kyc: submission %s: schedule the intent: %v", run.submissionID, err)
		} else if err := s.dispatcher.DispatchAfter(run.submissionID, customerID, provider, delay); err != nil {
			// The intent is already due for the sweeper; only the row is left.
			facades.Log().Errorf("kyc: submission %s: re-queue at %s: %v", run.submissionID, provider, err)
			s.release(run.row, run.isNew, run.submission.PreviousStatus, provider, run.started, failure)
			return
		} else {
			facades.Log().Warningf("kyc: submission %s: %s create retrying in %s: %v",
				run.submissionID, provider, delay, failure)
			return
		}
	}
	if err := s.intents.ScheduleRecovery(run.intent, now.Add(atomicLease), code); err != nil {
		facades.Log().Errorf("kyc: submission %s: schedule the intent for the sweeper: %v", run.submissionID, err)
	}
	s.release(run.row, run.isNew, run.submission.PreviousStatus, provider, run.started, failure)
}

// resumeAtomic is what a run of an atomic provider does when its staging is
// gone: the intent's prepared body is all a create needs, so a submission is
// rebuilt from it and sent. With no body prepared the images went before it
// was, and the intent fails with them.
//
// It reports whether it took the run over; false leaves it to abandon.
func (s *ProviderKycService) resumeAtomic(
	ctx context.Context, submissionID string, customerID uint, provider string,
	adapter ramp.ProviderAdapter, started time.Time,
) bool {
	creator, ok := adapter.(ramp.AtomicKycCreator)
	if !ok || s.intents == nil {
		return false
	}
	intent, material, err := s.intents.FindActive(customerID, provider)
	if err != nil {
		if errors.Is(err, repositories.ErrKycIntentIntegrity) && intent != nil && intent.SubmissionID == submissionID {
			if markErr := s.intents.MarkManualReview(intent, repositories.KycIntentCodeIntegrityFailure,
				time.Now().Add(s.intentRetention)); markErr != nil {
				facades.Log().Errorf("kyc: submission %s: hold the intent for review: %v", submissionID, markErr)
			}
		}
		return false
	}
	if intent == nil || intent.SubmissionID != submissionID {
		return false
	}
	if material.Prepared == nil || intent.MaterialClearedAt != nil {
		if intent.State != models.KycIntentManualReview {
			if err := s.intents.MarkFailed(intent, FailureCodeSubmissionExpired, time.Now()); err != nil {
				facades.Log().Errorf("kyc: submission %s: mark the intent failed: %v", submissionID, err)
			}
		}
		return false
	}
	link, err := s.integRepo.FindByCustomerAndProvider(customerID, provider)
	if err != nil || link == nil {
		return false
	}
	row, isNew, err := s.loadRow(customerID, provider, adapter)
	if err != nil {
		facades.Log().Errorf("kyc: submission %s: load row: %v", submissionID, err)
		return true
	}
	_ = s.processAtomicCustomer(ctx, &atomicRun{
		submissionID: submissionID,
		// Who sent it went with the staging. An atomic create is a person's
		// (an individual customer's, whose id is its user's: the providers
		// that create customers whole take no company), so the timeline names
		// that user.
		submission: &StagedSubmission{
			CustomerID: customerID, UserID: customerID, Provider: provider, Kind: KindAtomicCustomer, resumed: true,
		},
		row: row, isNew: isNew, link: link, creator: creator, started: started,
	})
	return true
}
