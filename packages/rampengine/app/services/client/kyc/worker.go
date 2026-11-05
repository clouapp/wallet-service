package kyc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
)

// submitFailedReason is what the client is told when a submission never reached
// the provider. It is a stable code rather than the upstream message: the app
// maps it to wording, and nothing an adapter or a provider wrote can leak into
// a user-visible string through this path.
const submitFailedReason = "submission_failed"

// FailureCodeSubmissionExpired is the failure code for a payload that outlived
// the worker's chance to send it: nothing was refused upstream, the images
// simply expired. It is this package's, not ramp's, because staging is.
const FailureCodeSubmissionExpired = "submission_expired"

// workerTimeout bounds one submission on the worker. It is generous because
// the payload carries images and the whole point of running here is not being
// bound by the HTTP handler's deadline.
const workerTimeout = 2 * time.Minute

// ProcessStagedSubmission is the worker half of a submission.
//
// customerID and provider travel in the queue argument alongside the staging id.
// They are routing, not payload — the identity documents stay sealed in Redis,
// which is the point of staging — and without them a submission whose staging
// expired could not be located at all: the row would sit on `submitting` with
// no event able to clear it and nothing told to the user.
//
// It always returns nil once it has an outcome to record. A queue retry cannot
// improve on what happened here: the payload was consumed, the user has been
// told over the event stream, and replaying would only re-send documents whose
// staging is already gone. The failure is logged for the operator instead.
// The one retry is this side's own: documents the provider refused for the
// moment are re-queued with their payload still staged (requeueTransient).
func (s *ProviderKycService) ProcessStagedSubmission(submissionID string, customerID uint, provider string) error {
	// The worker's own context, not the request's: see StagedSubmission.
	ctx, cancel := context.WithTimeout(context.Background(), workerTimeout)
	defer cancel()
	started := time.Now()

	adapter, err := s.factory.Get(provider)
	if err != nil {
		facades.Log().Errorf("kyc: submission %s: %v", submissionID, err)
		s.staging.Forget(submissionID)
		return nil
	}

	submission, err := s.staging.Get(submissionID)
	if err != nil {
		// An expired or unreadable payload cannot be recovered by retrying, but
		// the row it marked in flight still has to be released: nothing else
		// ever clears `submitting`, and the app is waiting on the event stream
		// for an outcome that would otherwise never arrive.
		//
		// An atomic create is the exception: once its body is prepared, the
		// intent holds everything the create needs, and the images are not
		// part of it.
		facades.Log().Errorf("kyc: staged submission %s unavailable: %v", submissionID, err)
		if s.resumeAtomic(ctx, submissionID, customerID, provider, adapter, started) {
			return nil
		}
		s.abandon(customerID, provider, adapter, started, err)
		return nil
	}

	link, err := s.integRepo.FindByCustomerAndProvider(customerID, provider)
	if err != nil || link == nil {
		facades.Log().Errorf("kyc: submission %s: integration missing for customer %d at %s: %v",
			submissionID, customerID, provider, err)
		s.staging.Forget(submissionID)
		s.abandon(customerID, provider, adapter, started, errIntegrationMissing)
		return nil
	}
	// Documents update a customer, so one has to exist — and in the wizard they
	// are queued moments after the application that creates it. The payload is
	// already staged, which is what lets the client send them once instead of
	// holding the images and re-uploading them on every attempt.
	//
	// So this yields rather than waits. Waiting held the only worker for the
	// whole worker timeout, and if this job was dequeued ahead of the
	// application that creates the customer, it was waiting on a job queued
	// behind it — a stall that could only end in the documents being dropped.
	row, isNew, err := s.loadRow(customerID, provider, adapter)
	if err != nil {
		facades.Log().Errorf("kyc: submission %s: load row: %v", submissionID, err)
		return nil
	}
	if submission.Kind == KindAtomicCustomer {
		creator, ok := adapter.(ramp.AtomicKycCreator)
		if !ok {
			facades.Log().Errorf("kyc: submission %s: %s does not create customers atomically", submissionID, provider)
			s.staging.Forget(submissionID)
			s.release(row, isNew, submission.PreviousStatus, provider, started, errNotAtomic)
			return nil
		}
		return s.processAtomicCustomer(ctx, &atomicRun{
			submissionID: submissionID, submission: submission, row: row, isNew: isNew,
			link: link, creator: creator, started: started,
		})
	}
	if submission.Kind == KindDocuments && !hasExternalCustomer(link) {
		if applicationInFlight(row, submission) {
			return s.requeueForCustomer(submissionID, row, isNew, submission.PreviousStatus, provider, started)
		}
		// No application is going to create the customer: it was refused,
		// or these documents were sent after it was. Waiting out the staging
		// TTL here is what kept the user on "submitting" for fifteen minutes
		// after the refusal had already been recorded on the row — and the
		// refusal is the answer, so the release announces it rather than
		// adding a failure of its own.
		//
		// That the refusal is on the row is what SubmitApplication guarantees,
		// on the queued path through releaseInFlight and on the pre-staging one
		// by writing it before it answers. It did not always: while the
		// synchronous refusal only travelled in the 409, this release left a
		// row saying `incomplete` with nothing on it at all.
		facades.Log().Warningf("kyc: submission %s: no application in flight to create a customer at %s; dropping documents",
			submissionID, provider)
		s.record(rampsvc.Entry{
			UserID: submission.UserID, CustomerID: customerID, Provider: provider, Operation: rampsvc.OpSubmitDocuments,
			SubmissionID: submissionID, SkipReason: "no_customer_to_update",
		})
		s.staging.Forget(submissionID)
		s.release(row, isNew, submission.PreviousStatus, provider, started, nil)
		return nil
	}
	// A business's people are placed by the ids Bridge gave them, which the
	// row holds once the application that named them landed — after the
	// documents were staged, in the wizard. Documents for a person with no id
	// yet wait for an application on its way, as they wait for the customer;
	// with none coming they cannot be placed, and Bridge would refuse the
	// whole call.
	if submission.Kind == KindDocuments && unplaced(submission.Documents, row.PersonIDs()) {
		if applicationInFlight(row, submission) {
			return s.requeueForCustomer(submissionID, row, isNew, submission.PreviousStatus, provider, started)
		}
		facades.Log().Warningf("kyc: submission %s: a person's documents have no provider id to go on at %s",
			submissionID, provider)
		s.record(rampsvc.Entry{
			UserID: submission.UserID, CustomerID: customerID, Provider: provider, Operation: rampsvc.OpSubmitDocuments,
			SubmissionID: submissionID, SkipReason: "person_not_named",
		})
		s.staging.Forget(submissionID)
		s.release(row, isNew, submission.PreviousStatus, provider, started, errPersonUnplaced)
		return nil
	}
	// What the database holds, before applyKycState rewrites the row in
	// memory. The release below has to decide against the stored status: on
	// the persist-failure path the in-memory row is already the state that
	// failed to be written, which would make it look like nothing is in
	// flight. The whole row is kept because the release also has to undo the
	// compliance fields — persisting endorsements for a customer this side
	// could not link to would advertise a rail no call can reach.
	stored := *row

	sent := time.Now()
	state, err := s.send(ctx, adapter, ramp.NewCustomerRef(customerID, link.ExternalCustomerID),
		submissionID, submission, row.PersonIDs())
	s.record(rampsvc.Entry{
		UserID: submission.UserID, CustomerID: customerID, Provider: provider, Operation: operationFor(submission.Kind),
		SubmissionID: submissionID, Started: sent, Err: err,
	})
	if err != nil && s.requeueTransient(submissionID, customerID, provider, submission, err) {
		return nil
	}
	s.staging.Forget(submissionID)

	if err != nil {
		// Nothing changed upstream, so the compliance state is what it was. The
		// error text is logged, never broadcast: only this side knows it carries
		// no customer data.
		facades.Log().Errorf("kyc: submission %s failed for customer %d at %s: %v",
			submissionID, customerID, provider, err)
		// See releaseInFlight: a no-op if another worker already advanced the row.
		moved := s.releaseInFlight(submissionID, row, &stored, submission.PreviousStatus, isNew, err)
		ramp.LogKycSubmission(provider, submitFailedReason, started)
		if !moved {
			s.publish(row, submitFailedReason)
			s.announceRefusal(stored.KycStatus, row, err)
		}
		return nil
	}

	// A provider that accepted the submission without reporting a status is
	// reviewing it. Saying so beats leaving the row on `submitting`, which no
	// later event would ever clear.
	if state.Status == "" {
		state.Status = models.KycStatusUnderReview
	}
	// applyKycState clears the last refusal: the provider has answered, which
	// is what makes any earlier one stale.
	applyKycState(row, state)
	written, err := s.linkAndSave(link, state.ExternalCustomerID, row, isNew, models.KycStatusSubmitting)
	if err != nil {
		facades.Log().Errorf("kyc: submission %s: persist state: %v", submissionID, err)
		// The write is the only thing that failed, and nothing later clears
		// `submitting`: no webhook fires for a row the provider does not know
		// we hold, a refresh has no customer to read, and a retry is refused
		// as a submission already in flight. Leaving it there is a permanently
		// stuck account, so the row falls back to what the user can act on —
		// the same release a failed send performs, for the same reason.
		moved := s.releaseInFlight(submissionID, row, &stored, submission.PreviousStatus, isNew, err)
		ramp.LogKycSubmission(provider, submitFailedReason, started)
		if !moved {
			s.publish(row, submitFailedReason)
			s.announceRefusal(stored.KycStatus, row, err)
		}
		return nil
	}
	ramp.LogKycSubmission(provider, row.KycStatus, started)
	// The row left `submitting` while the provider answered — an admin reset
	// of a row past the staging TTL, a webhook, a refresh. That is newer than
	// this answer, which read the row before the send: it is not written over
	// it and not announced. The customer the send created is on the link.
	if !written {
		facades.Log().Warningf("kyc: submission %s: the row at %s moved during the send; its answer is not written over it",
			submissionID, provider)
		return nil
	}
	s.publish(row, "")
	s.notifier.KycUpdated(stored.KycStatus, row)
	return nil
}

// operationFor names the act a staged submission performs, in the vocabulary
// the timeline reads in.
func operationFor(kind string) string {
	if kind == KindDocuments || kind == KindAtomicCustomer {
		return rampsvc.OpSubmitDocuments
	}
	return rampsvc.OpSubmitApplication
}

// abandon releases a row marked in flight for a submission that can no longer
// be sent, and tells the app so.
//
// It runs when the payload is gone or the integration vanished — cases where
// there is nothing to send and nothing upstream changed. A row left on
// `submitting` is the worst outcome available: guardInFlight eventually lets a
// resubmit through, but until then the user watches a status no event will move.
//
// The failure, when there is one, is recorded on the row on the way out. A nil
// failure keeps whatever the row already says — the case of documents dropped
// because the application ahead of them was refused, where the refusal is the
// answer and the drop adds nothing to it.
func (s *ProviderKycService) abandon(customerID uint, provider string, adapter ramp.ProviderAdapter, started time.Time, failure error) {
	row, isNew, err := s.loadRow(customerID, provider, adapter)
	if err != nil {
		facades.Log().Errorf("kyc: release in-flight row for customer %d at %s: %v", customerID, provider, err)
		return
	}
	// The payload, and with it the status the row was marked from, is gone.
	s.release(row, isNew, "", provider, started, failure)
}

// release is abandon for a row already in hand. previous is the status the
// submission marked the row from, when known: documents dropped for want of a
// customer put back the refusal they found rather than `incomplete`, which
// erased what the app was showing. Unknown, it falls back as restoreStatus
// says.
//
// The write lands only while the row is still `submitting` (saveRowIf): the
// row was read before this decision, and an answer written since is newer
// than anything a release knows. Nothing is announced then either.
func (s *ProviderKycService) release(
	row *models.CustomerProviderKyc, isNew bool, previous, provider string, started time.Time, failure error,
) {
	if row.KycStatus != models.KycStatusSubmitting {
		return
	}
	row.KycStatus = restoreStatus(previous)
	if failure != nil {
		row.RecordSubmissionFailure(failureCode(failure), failureDetail(failure))
	}
	written, err := s.saveRowIf(row, isNew, models.KycStatusSubmitting)
	if err != nil {
		facades.Log().Errorf("kyc: release in-flight row for customer %d at %s: %v", row.CustomerID, provider, err)
		return
	}
	if !written {
		return
	}
	ramp.LogKycSubmission(provider, submitFailedReason, started)
	s.publish(row, submitFailedReason)
}

// releaseInFlight takes a row back off `submitting` and writes it. It is a
// no-op on a row some other worker has already advanced: documents queued
// behind an application capture `submitting` as their previous status, and
// restoreStatus maps that to `incomplete` — so a documents failure arriving
// after the application worker moved the row to under_review or approved
// would write `incomplete` over it, regressing the user's compliance state
// and closing rails their endorsements had already opened.
//
// The failure is recorded on the row as it is released: the request that
// queued this submission answered 202 long ago, and the row is the only place
// the user and the admin can still find out what went wrong.
//
// stored was read before the send, up to workerTimeout earlier, so the write
// lands only while the row is still `submitting` (saveRowIf). It reports
// whether the row moved in the meantime and the write missed: row is then a
// copy of something the database no longer holds, and must not be announced.
func (s *ProviderKycService) releaseInFlight(
	submissionID string, row *models.CustomerProviderKyc, stored *models.CustomerProviderKyc,
	previousStatus string, isNew bool, failure error,
) (moved bool) {
	if stored.KycStatus != models.KycStatusSubmitting {
		return s.recordOnSettledRow(submissionID, row, stored, failure)
	}
	// Nothing this submission read may survive the release. On the send
	// failure there is nothing to undo; on the persist failure applyKycState
	// has already written the provider's answer into the row, and keeping it
	// would leave endorsements — and therefore an open rail — recorded for a
	// customer the link row cannot name.
	*row = *stored
	row.KycStatus = restoreStatus(previousStatus)
	row.RecordSubmissionFailure(failureCode(failure), failureDetail(failure))
	applyRefusal(row, failure)
	applyDocumentRefusal(row, failure)
	// A refusal on the terms invalidates what this side believed about them,
	// and it has to be applied after the copy above or the copy undoes it.
	//
	// Without this the row stays `approved` holding an agreement id the
	// provider will not honour. StartTosForEnabled skips a provider whose
	// terms are approved, so the step never reappears; the submission gate is
	// satisfied by the id, so every resubmission sends the same dead value and
	// is refused identically. There is no path out of that on its own.
	if isTosRefusal(failure) {
		row.ResetTos()
	}
	written, err := s.saveRowIf(row, isNew, models.KycStatusSubmitting)
	if err != nil {
		facades.Log().Errorf("kyc: submission %s: restore status: %v", submissionID, err)
		return false
	}
	return !written
}

// recordOnSettledRow records a failure on a row no longer in flight: the
// application ahead of these documents landed and settled it (incomplete,
// with what the provider still asks) before the documents were refused.
// Left unrecorded, the row said nothing of the refusal and the app had
// nothing to tell the user (PF documents investigation, 19/10/2026). The
// provider's status stands; the write lands only while the row still holds
// the status read before the send, and it reports whether the row moved.
func (s *ProviderKycService) recordOnSettledRow(
	submissionID string, row *models.CustomerProviderKyc, stored *models.CustomerProviderKyc, failure error,
) (moved bool) {
	*row = *stored
	row.RecordSubmissionFailure(failureCode(failure), failureDetail(failure))
	applyDocumentRefusal(row, failure)
	written, err := s.saveRowIf(row, false, stored.KycStatus)
	if err != nil {
		facades.Log().Errorf("kyc: submission %s: record the failure: %v", submissionID, err)
		return false
	}
	return !written
}

// applyDocumentRefusal names a document the provider refused as it was sent
// with the category whose words the app has and whose step it reopens
// (document_unreadable), leaving the status the provider gave: the provider
// refused a file, not the customer, so nobody is told of a rejection.
func applyDocumentRefusal(row *models.CustomerProviderKyc, failure error) {
	if errors.Is(failure, ramp.ErrDocumentRefused) {
		row.SetRejectionCategories([]string{models.KycRejectionDocumentUnreadable})
	}
}

// applyRefusal records a create the provider refused over who the applicant is
// as the rejection it is. Put back where it was, the row would read as never
// started and owe the whole application again — which the app asks for, and
// the provider refuses identically, on every visit. The failure recorded
// beside it still carries the provider's text and request id for the admin.
func applyRefusal(row *models.CustomerProviderKyc, failure error) {
	var refusal *ramp.KycRefusal
	if !errors.As(failure, &refusal) {
		return
	}
	row.KycStatus = models.KycStatusRejected
	row.SetRejectionCategories(refusal.Categories)
	row.SetRejectionDetails(refusal.Details)
}

// announceRefusal tells the user's devices about a refusal a release recorded:
// it is this submission's verdict, and how a rejection most often arrives --
// after the request that sent it has long answered. Any other failure only
// put the row back, which is no news.
func (s *ProviderKycService) announceRefusal(previous string, row *models.CustomerProviderKyc, failure error) {
	var refusal *ramp.KycRefusal
	if errors.As(failure, &refusal) {
		s.notifier.KycUpdated(previous, row)
	}
}

// isTosRefusal reports whether the provider turned the submission away over its
// terms of service, in any of the three ways it can say so.
//
// ErrTosRequired is "no accepted terms on this customer"; the other two are
// "the session you named is dead". All three leave the same wreckage on the
// row, so all three clear it. A provider whose terms are genuinely unsigned
// answers the first, and a row that never held a session is unaffected —
// ResetTos on an already-pending row writes what is already there.
//
// The adapter is not consulted: only a provider that has terms can refuse over
// them, and ResetTos leaves a `not_required` row alone by itself.
func isTosRefusal(err error) bool {
	return errors.Is(err, ramp.ErrTosRequired) ||
		errors.Is(err, ramp.ErrTosSessionExpired) ||
		errors.Is(err, ramp.ErrTosSessionUnknown)
}

// restoreStatus is what a row goes back to when a submission never reached the
// provider.
//
// Normally that is the status the row held before it was marked in flight. But
// a stale retry — one guardInFlight let through past the staging TTL — captures
// `submitting` as its own previous status, and restoring that would weld the
// value to the row: every later failure would restore `submitting` again.
// `incomplete` is the honest fallback, and true besides: the user started, and
// the provider holds nothing finished.
func restoreStatus(previous string) string {
	if previous == "" || previous == models.KycStatusSubmitting {
		return models.KycStatusIncomplete
	}
	return previous
}

// applicationInFlight reports whether an application is still queued ahead of
// these documents to create the customer they update.
//
// Two facts have to agree. The documents captured `submitting` as their
// previous status — an application was in flight when they were staged — and
// the row still says so: a refused application releases the row to
// `incomplete`, after which nothing is going to create a customer. Documents
// staged after the refusal captured `incomplete` instead, and fail the first
// half.
func applicationInFlight(row *models.CustomerProviderKyc, submission *StagedSubmission) bool {
	return submission.PreviousStatus == models.KycStatusSubmitting &&
		row.KycStatus == models.KycStatusSubmitting
}

// errIntegrationMissing is the failure recorded when the link row vanished
// between the request and the worker.
var errIntegrationMissing = errors.New("kyc: integration missing")

// errNotAtomic is the failure an atomic create is released with when its
// provider does not create customers atomically: a wiring fault on this side,
// recorded as the default code, not a hold.
var errNotAtomic = errors.New("kyc: the provider does not create customers atomically")

// errCustomerMissing is the failure recorded when documents have no customer
// to attach to and no way to wait for one.
var errCustomerMissing = errors.New("kyc: no customer to attach documents to")

// errPersonUnplaced is the failure recorded when a business's documents name
// a person the provider holds no id for, and no application is coming to
// give them one.
var errPersonUnplaced = errors.New("kyc: a person's documents have no provider id to go on")

// unplaced reports whether docs name a person people holds no id for.
func unplaced(docs *ramp.Documents, people map[string]string) bool {
	if docs == nil {
		return false
	}
	for _, person := range docs.PersonDocuments {
		if people[person.LocalRef] == "" {
			return true
		}
	}
	return false
}

// failureCode names a submission failure in the vocabulary the HTTP layer
// uses, so a refusal recorded by the worker reads the same as one answered
// in the request.
func failureCode(err error) string {
	if errors.Is(err, ErrStagingExpired) {
		return FailureCodeSubmissionExpired
	}
	// The codes the HTTP layer answers these with, so a refusal the worker
	// records reads like the one a request would have been given.
	if errors.Is(err, ErrSubmissionInFlight) {
		return "submission_in_flight"
	}
	if errors.Is(err, ErrApplicationRequired) {
		return "application_required"
	}
	if errors.Is(err, ErrSubmissionHeld) {
		return "submission_held"
	}
	if code := ramp.ErrorCode(err); code != "" {
		return code
	}
	return ramp.DefaultErrorCode
}

// failureDetailLimit bounds what is stored of the upstream text. It is for an
// operator to read, not a payload to archive.
const failureDetailLimit = 500

func failureDetail(err error) string {
	detail := err.Error()
	if len(detail) > failureDetailLimit {
		detail = detail[:failureDetailLimit]
	}
	return detail
}

// --- internals --------------------------------------------------------------

// send performs the adapter call the staged submission asked for. The
// submission id is the idempotency key: generated once, carried in the queue
// argument, and therefore stable across attempts at this submission while
// differing from the next one.
//
// people is the provider's ids for a business's people (the row's
// PersonIDs): each person the submission names goes by theirs, so a known
// one is updated rather than created again and their documents land on them.
func (s *ProviderKycService) send(
	ctx context.Context,
	adapter ramp.ProviderAdapter,
	ref ramp.CustomerRef,
	submissionID string,
	submission *StagedSubmission,
	people map[string]string,
) (*ramp.KycState, error) {
	switch submission.Kind {
	case KindApplication:
		if submission.Application == nil {
			return nil, errors.New("kyc: staged application is empty")
		}
		app := *submission.Application
		app.IdempotencyKey = "kyc-app:" + submissionID
		app.AssociatedPersons = slices.Clone(app.AssociatedPersons)
		for i := range app.AssociatedPersons {
			app.AssociatedPersons[i].ExternalID = people[app.AssociatedPersons[i].LocalRef]
		}
		return adapter.SubmitApplication(ctx, ref, app)
	case KindDocuments:
		if submission.Documents == nil {
			return nil, errors.New("kyc: staged documents are empty")
		}
		docs := *submission.Documents
		docs.IdempotencyKey = "kyc-docs:" + submissionID
		docs.PersonDocuments = slices.Clone(docs.PersonDocuments)
		for i := range docs.PersonDocuments {
			docs.PersonDocuments[i].ExternalID = people[docs.PersonDocuments[i].LocalRef]
		}
		return adapter.SubmitDocuments(ctx, ref, docs)
	default:
		return nil, fmt.Errorf("kyc: unknown submission kind %q", submission.Kind)
	}
}

// awaitCustomerInterval is the re-queue delay of a documents submission whose
// customer the application job ahead of it has not created yet.
const awaitCustomerInterval = time.Second

// The bounds of the wait before documents a provider refused for the moment
// are sent again. The floor gives a customer the provider is still processing
// time to settle (Bridge took about twenty seconds on 01/10/2026); the ceiling
// keeps several attempts inside StagingTTL.
const (
	transientRetryFloor   = 5 * time.Second
	transientRetryCeiling = time.Minute
	// transientRetryWindow is how late into StagingTTL an attempt may still be
	// scheduled. The ceiling's worth of slack absorbs a queue running behind:
	// an attempt dequeued after the payload expired takes the expiry path,
	// which has nothing to tell the app.
	transientRetryWindow = StagingTTL - transientRetryCeiling
)

// requeueTransient puts documents back on the queue when the provider refused
// them for the moment, and reports whether it did.
//
// Measured on 01/10/2026: Bridge refused documents sent seconds after the
// application created the customer, the worker dropped them, and the user —
// told 202 — was asked for documents nothing would ever send. A refusal the
// provider calls temporary, or a request that provably never left, is the same
// submission later. The payload stays staged and its in-flight mark with it,
// so the overview keeps saying the documents are on their way, and nothing is
// published: there is no outcome yet.
//
// Applications are left alone: their failure reaches the app while the form is
// still in hand. Documents are what went missing after a 202.
func (s *ProviderKycService) requeueTransient(
	submissionID string, customerID uint, provider string, submission *StagedSubmission, failure error,
) bool {
	if submission.Kind != KindDocuments || !isTransient(failure) || s.dispatcher.RunsInline() {
		return false
	}
	delay, ok := transientRetryDelay(submission.StagedAt, time.Now())
	if !ok {
		return false
	}
	if err := s.dispatcher.DispatchAfter(submissionID, customerID, provider, delay); err != nil {
		facades.Log().Errorf("kyc: submission %s: re-queue documents at %s: %v", submissionID, provider, err)
		return false
	}
	facades.Log().Warningf("kyc: submission %s: %s refused documents for now, retrying in %s: %v",
		submissionID, provider, delay, failure)
	return true
}

// isTransient reports whether the same submission is expected to go through
// later: the provider said so, or the request never reached it.
func isTransient(err error) bool {
	return errors.Is(err, ramp.ErrRetryableUpstream) || errors.Is(err, ramp.ErrRequestNotSent)
}

// transientRetryDelay is how long to wait before the next attempt — as long as
// the submission has existed, between the floor and the ceiling, so the waits
// roughly double — or false when that attempt would land past
// transientRetryWindow. A payload with no StagedAt predates the field: its age
// saturates at the longest Duration, which is out of time.
func transientRetryDelay(stagedAt, now time.Time) (time.Duration, bool) {
	age := now.Sub(stagedAt)
	delay := min(max(age, transientRetryFloor), transientRetryCeiling)
	if age > transientRetryWindow-delay {
		return 0, false
	}
	return delay, true
}

// hasExternalCustomer reports whether the integration names a provider-side customer.
func hasExternalCustomer(link *models.CustomerProviderIntegration) bool {
	return link != nil && link.ExternalCustomerID != nil && *link.ExternalCustomerID != ""
}

// requeueForCustomer puts the submission back on the queue to try again once
// the application ahead of it has created the customer.
//
// The ceiling is the staging TTL: past it the images are gone, the next
// attempt takes the expiry branch, and that releases the row.
//
// The sync driver runs jobs inline, so there is no worker to free and the
// application has already finished by the time documents are dispatched: a
// missing customer there means it genuinely failed, and re-queueing would
// recurse rather than wait.
func (s *ProviderKycService) requeueForCustomer(
	submissionID string, row *models.CustomerProviderKyc, isNew bool,
	previous, provider string, started time.Time,
) error {
	if s.dispatcher.RunsInline() {
		facades.Log().Errorf("kyc: submission %s: no customer at %s to attach documents to",
			submissionID, provider)
		s.staging.Forget(submissionID)
		s.release(row, isNew, previous, provider, started, errCustomerMissing)
		return nil
	}
	if err := s.dispatcher.DispatchAfter(submissionID, row.CustomerID, provider, awaitCustomerInterval); err != nil {
		facades.Log().Errorf("kyc: submission %s: re-queue for customer at %s: %v",
			submissionID, provider, err)
		s.staging.Forget(submissionID)
		s.release(row, isNew, previous, provider, started, fmt.Errorf("kyc: re-queue: %w", err))
	}
	return nil
}
