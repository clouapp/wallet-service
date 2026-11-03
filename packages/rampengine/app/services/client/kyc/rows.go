package kyc

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
)

// stagedSubmit is the in-flight handoff between the advisory lock (stage +
// persist) and the queue dispatch that must run after the lock is released.
type stagedSubmit struct {
	submissionID string
	row          *models.CustomerProviderKyc
	submission   StagedSubmission
}

// held reports whether the submission was accepted without anything to
// dispatch: an application deferred until the documents (deferApplication).
func (s stagedSubmit) held() bool {
	return s.submissionID == "" && s.row != nil
}

// stageSubmission seals the payload and marks the row in flight. The row is
// persisted before dispatch so a worker that starts immediately never reads a
// row that still says not_started.
//
// The mark lands only on the status it was decided from, submission's
// PreviousStatus. The request holds the submit lock, but the worker that
// resolves an earlier submission writes outside it: a mark written from the
// copy read before that worker landed undid the provider's answer (30/09,
// a final refusal marked in flight again a millisecond after it was
// recorded). A miss is errRowMoved, and the payload is let go.
func (s *ProviderKycService) stageSubmission(row *models.CustomerProviderKyc, isNew bool, submission StagedSubmission) (stagedSubmit, error) {
	submissionID, err := s.staging.Put(submission)
	if err != nil {
		return stagedSubmit{}, err
	}
	row.KycStatus = models.KycStatusSubmitting
	written, err := s.saveRowIf(row, isNew, submission.PreviousStatus)
	if err != nil || !written {
		s.staging.Forget(submissionID)
		return stagedSubmit{}, cmp.Or(err, errRowMoved)
	}
	return stagedSubmit{submissionID: submissionID, row: row, submission: submission}, nil
}

// errRowMoved is a write decided from a status the row no longer holds.
var errRowMoved = errors.New("kyc: the row moved since it was read")

// maxStageAttempts bounds how often a request reads the row again after its
// write missed. Only the writers outside the lock can move it — a worker, a
// webhook — and each writes once, so a second read settles it; a row still
// moving after this is answered as a submission in flight, which the client
// already retries.
const maxStageAttempts = 3

// untilSettled runs a locked read-decide-write again while its write misses
// because the row moved in between, so the decision is always taken on what
// the row holds now: a refusal that landed is answered as the refusal.
func untilSettled(attempt func() error) error {
	for range maxStageAttempts {
		if err := attempt(); !errors.Is(err, errRowMoved) {
			return err
		}
	}
	return ErrSubmissionInFlight
}

// dispatchStagedSubmission hands a staged submission to a worker after the
// advisory lock is released. With the sync queue driver the worker finishes
// before Dispatch returns and the row is reloaded so the 202 body matches DB.
func (s *ProviderKycService) dispatchStagedSubmission(staged stagedSubmit) (*models.CustomerProviderKyc, error) {
	if err := s.dispatcher.Dispatch(staged.submissionID, staged.submission.CustomerID, staged.submission.Provider); err != nil {
		s.staging.Forget(staged.submissionID)
		staged.row.KycStatus = restoreStatus(staged.submission.PreviousStatus)
		// Only off the mark this request wrote: a worker may already have
		// written an answer over it.
		if _, saveErr := s.saveRowIf(staged.row, false, models.KycStatusSubmitting); saveErr != nil {
			facades.Log().Errorf("kyc: restore status after failed dispatch: %v", saveErr)
		}
		return nil, fmt.Errorf("kyc: queue submission: %w", err)
	}
	if s.dispatcher.RunsInline() {
		return s.reloadRowAfterQueue(staged.row)
	}
	return staged.row, nil
}

// reloadRowAfterQueue reads the persisted row after a (possibly inline) worker
// run. On lookup failure the staged pointer is returned so the handler can
// still answer 202.
func (s *ProviderKycService) reloadRowAfterQueue(row *models.CustomerProviderKyc) (*models.CustomerProviderKyc, error) {
	reloaded, err := s.kycRepo.FindByCustomerAndProvider(row.CustomerID, row.Provider)
	if err != nil {
		facades.Log().Errorf("kyc: reload row after queue for customer %d at %s: %v",
			row.CustomerID, row.Provider, err)
		return row, nil
	}
	if reloaded == nil {
		return row, nil
	}
	return reloaded, nil
}

// linkAndSave commits the customer link and the KYC row together.
//
// Apart, a failure between them leaves one of two broken shapes: a customer
// linked upstream whose KYC row does not record the link — so the next
// HostedLink call issues a second page at the provider — or a KYC row that
// addresses a customer nothing was ever linked to, which makes every later
// call unable to name it.
//
// The row is written only while its stored status is still status — the one
// the caller read before its provider call — and it reports whether it was:
// whatever landed on the row meanwhile (a worker's answer, a webhook, an
// admin reset) is newer than the caller's copy. The link is written either
// way, because the customer it names exists upstream regardless.
func (s *ProviderKycService) linkAndSave(
	link *models.CustomerProviderIntegration,
	externalCustomerID string,
	row *models.CustomerProviderKyc,
	isNew bool,
	status string,
) (bool, error) {
	linked := link.ExternalCustomerID != nil && *link.ExternalCustomerID == externalCustomerID &&
		link.Status == models.IntegrationStatusActive
	if externalCustomerID == "" || linked {
		return s.saveRowIf(row, isNew, status)
	}
	// The provider-side customer id is what turns a pending link into a usable one.
	previousID, previousStatus := link.ExternalCustomerID, link.Status
	link.ExternalCustomerID = &externalCustomerID
	link.Status = models.IntegrationStatusActive
	written, err := s.kycRepo.SaveWithLink(row, isNew, link, status)
	if err != nil {
		// Nothing was written: the caller's copy stays the link it read.
		link.ExternalCustomerID, link.Status = previousID, previousStatus
		// An operator unlinked the provider since the link was read: there is
		// no customer left to keep, and the user starts over.
		if errors.Is(err, repositories.ErrIntegrationGone) {
			return false, fmt.Errorf("kyc: persist: %w", ramp.ErrIntegrationRequired)
		}
		return false, fmt.Errorf("kyc: persist: %w", err)
	}
	return written, nil
}

// linkedCustomer resolves the adapter and the customer a call about an
// existing link addresses. The registry comes first because it costs no
// query, and an unknown provider should not buy one.
func (s *ProviderKycService) linkedCustomer(customerID uint, provider string) (ramp.ProviderAdapter, ramp.CustomerRef, error) {
	adapter, err := s.factory.Get(provider)
	if err != nil {
		return nil, ramp.CustomerRef{}, err
	}
	link, err := s.integRepo.FindByCustomerAndProvider(customerID, provider)
	if err != nil {
		return nil, ramp.CustomerRef{}, fmt.Errorf("kyc: lookup integration: %w", err)
	}
	if link == nil {
		return nil, ramp.CustomerRef{}, ramp.ErrIntegrationRequired
	}
	return adapter, ramp.NewCustomerRef(customerID, link.ExternalCustomerID), nil
}

// loadRow returns the customer's KYC row, or a fresh unsaved one. The bool reports
// which, so the caller inserts instead of updating.
func (s *ProviderKycService) loadRow(customerID uint, provider string, adapter ramp.ProviderAdapter) (*models.CustomerProviderKyc, bool, error) {
	row, err := s.kycRepo.FindByCustomerAndProvider(customerID, provider)
	if err != nil {
		return nil, false, fmt.Errorf("kyc: lookup: %w", err)
	}
	if row == nil {
		return s.newRow(customerID, provider, adapter), true, nil
	}
	return row, false, nil
}

// saveRowIf writes the row decided from its status: it lands only while the
// stored row still holds status, and reports whether it did. Every write of
// an existing row goes through here — each is decided from a read that a
// worker, a webhook or an admin can overtake. A new row has nothing to have
// moved.
func (s *ProviderKycService) saveRowIf(row *models.CustomerProviderKyc, isNew bool, status string) (bool, error) {
	if isNew {
		if err := s.kycRepo.Create(row); err != nil {
			return false, fmt.Errorf("kyc: persist: %w", err)
		}
		return true, nil
	}
	written, err := s.kycRepo.UpdateIfStatus(row, status)
	if err != nil {
		return false, fmt.Errorf("kyc: persist: %w", err)
	}
	return written, nil
}

// saveRowUnmoved is saveRowIf for a write that does not decide the status —
// the terms fields, a failure record — but was read with the rest of the row:
// it lands only on the status it read, and answers errRowMoved otherwise, so
// the caller reads again (untilSettled) instead of writing a stale status
// back over whatever landed in between.
func (s *ProviderKycService) saveRowUnmoved(row *models.CustomerProviderKyc, isNew bool) error {
	written, err := s.saveRowIf(row, isNew, row.KycStatus)
	if err != nil {
		return err
	}
	if !written {
		return errRowMoved
	}
	return nil
}

// publish pushes the row's user-shareable fields to the live app of every
// user who acts for its customer, naming the customer. Delivery is
// best-effort by design; the row stays the source of truth, and a failure to
// read who to tell is logged rather than failing what already committed.
func (s *ProviderKycService) publish(row *models.CustomerProviderKyc, failureReason string) {
	s.audience.Kyc(broadcast.KycEvent{
		CustomerID:       row.CustomerID,
		Provider:         row.Provider,
		KycStatus:        row.KycStatus,
		TosStatus:        row.TosStatus,
		CollectionMode:   row.CollectionMode,
		HostedKycURL:     row.HostedKycURL,
		Requirements:     decodeStrings(row.Requirements),
		RejectionReasons: decodeStrings(row.RejectionReasons),
		FailureReason:    failureReason,
		FailureCode:      failureCodeFor(row, failureReason),
	})
}

// failureCodeFor is the row's last failure code, on a failure event only: a
// success frame must not carry the refusal it just cleared, and a settled row
// still holding an old code is not announcing it.
func failureCodeFor(row *models.CustomerProviderKyc, failureReason string) string {
	if failureReason == "" || row.LastErrorCode == nil {
		return ""
	}
	return *row.LastErrorCode
}

// guardInFlight rejects a second submission while one is queued.
//
// It takes the kind because the two are not the same question. A second
// application while one is in flight is a duplicate — a second device, or a
// double tap — and must be refused. Documents arriving while the application is
// in flight are the normal wizard order, not a duplicate: refusing them is what
// forced the client to poll for sixty seconds before daring to send them, and
// what a client that stopped polling would silently lose its documents to.
//
// Which is why it asks what is actually in flight. `submitting` says something
// is queued and not what, and documents set it too — so letting every documents
// submission through, as this did, removed their duplicate check entirely
// rather than narrowing it to the chaining case.
//
// The mark is read only here, on the one path that needs it: a row that is not
// submitting has nothing to chain behind, and an application never chains.
//
// A missing mark reads as "let it through". Refusing on one would put the
// client back to polling for a customer before daring to send its documents,
// which is the failure this whole path exists to remove — and the row status is
// still the outer guard.
//
// The staleness escape matters for applications: a worker that never ran would
// otherwise wedge the user on `submitting` forever. Past the staging TTL the
// payload cannot exist any more, so the earlier submission provably cannot
// complete.
func (s *ProviderKycService) guardInFlight(
	row *models.CustomerProviderKyc, kind string, customerID uint, provider string,
) error {
	if row.KycStatus != models.KycStatusSubmitting {
		return nil
	}
	if kind == KindDocuments && s.staging.InFlightKind(customerID, provider) != KindDocuments {
		return nil
	}
	if stuckSubmission(row, time.Now()) {
		return nil
	}
	return ErrSubmissionInFlight
}

// guardTerminal refuses anything sent to a provider that refused the user for
// good (Terminal): a submission, a hosted link, an endorsement request, a terms
// session. The fan-outs pass such a provider over before this point; the
// provider routes name it directly, and it never receives this user's data
// again whichever way it is asked. The row is marked first: whether an
// unclassified rejection still owes the application is part of the answer.
func (s *ProviderKycService) guardTerminal(row *models.CustomerProviderKyc, holdsExternalCustomer bool) error {
	markApplication(row, holdsExternalCustomer, s.factory)
	if Terminal(row) {
		return ramp.ErrKycRejected
	}
	return nil
}

// newRow is newKycRow for customerID: a business's row is the company's
// (Business), as a stored one reads back from the repository.
func (s *ProviderKycService) newRow(customerID uint, provider string, adapter ramp.ProviderAdapter) *models.CustomerProviderKyc {
	row := newKycRow(customerID, provider, adapter)
	row.Business = s.isBusiness(customerID)
	return row
}

// isBusiness reports whether customerID is a business. A failed read logs and
// answers no: what it decides is which wizard's steps a row names, and a
// screen that degrades beats a refusal nobody can act on.
func (s *ProviderKycService) isBusiness(customerID uint) bool {
	business, err := s.businessCustomer(customerID)
	if err != nil {
		facades.Log().Errorf("kyc: %v", err)
	}
	return business
}

// businessCustomer is isBusiness for a call that acts on the answer, where a
// failed read is an error.
func (s *ProviderKycService) businessCustomer(customerID uint) (bool, error) {
	if s.persons.Customers == nil {
		return false, nil
	}
	customer, err := s.persons.Customers.FindByID(customerID)
	if err != nil {
		return false, fmt.Errorf("reading customer %d: %w", customerID, err)
	}
	return customer != nil && customer.Type == models.CustomerTypeBusiness, nil
}

// newKycRow builds an unsaved row in the provider's preferred collection mode.
func newKycRow(customerID uint, provider string, adapter ramp.ProviderAdapter) *models.CustomerProviderKyc {
	mode := adapter.PreferredKycMode().String()
	tosStatus := models.TosStatusNotRequired
	if adapter.RequiresTos() {
		tosStatus = models.TosStatusPending
	}
	return &models.CustomerProviderKyc{
		CustomerID:     customerID,
		Provider:       provider,
		KycStatus:      models.KycStatusNotStarted,
		PreferredMode:  mode,
		CollectionMode: mode,
		TosStatus:      tosStatus,
		// The jsonb columns are NOT NULL; an absent value is an empty document.
		Endorsements:        "[]",
		Requirements:        "[]",
		RejectionReasons:    "[]",
		RejectionCategories: "[]",
		RejectionDetails:    "[]",
		RawStatus:           "{}",
	}
}

// applyKycState merges a provider snapshot onto the local row.
func applyKycState(row *models.CustomerProviderKyc, state *ramp.KycState) {
	row.KycStatus = orDefault(state.Status, row.KycStatus)
	row.TosStatus = orDefault(state.TosStatus, row.TosStatus)
	if state.SignedAgreementID != "" {
		row.SignedAgreementID = &state.SignedAgreementID
	}
	if err := row.SetEndorsements(state.Endorsements); err != nil {
		row.Endorsements = "[]"
	}
	row.SetRequirements(state.Requirements)
	// A business's people at the provider: named by a call that sent them,
	// kept as they were by one that only read the customer back.
	if state.AssociatedPersonIDs != nil {
		row.SetPersonIDs(state.AssociatedPersonIDs)
	}
	row.SetRejectionReasons(state.RejectionReasons)
	row.SetRejectionCategories(state.RejectionCategories)
	row.SetRejectionDetails(state.RejectionDetails)
	if len(state.Raw) > 0 {
		row.RawStatus = string(state.Raw)
	}
	// The provider has answered about this customer, so the last refusal is no
	// longer the current story — and shown next to a fresh answer it reads as
	// one, which is the failure ClearSubmissionFailure exists to prevent.
	//
	// Cleared here rather than only on an accepted submission because a
	// refusal does not always mean the submission failed to land: a create
	// answered with 500 had in fact created the customer (02/09), and the
	// refresh that finally read it back left the refusal standing, driving the
	// app to tell the user to start over. Anything the user still owes travels
	// in Requirements, which this same call has just rewritten.
	row.ClearSubmissionFailure()
	now := time.Now()
	row.LastSyncedAt = &now
}

// orDefault keeps a persisted value when the provider reported nothing.
func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func lockKey(op string, customerID uint, provider string) string {
	return fmt.Sprintf("kyc:%s:%d:%s", op, customerID, provider)
}

// derivedKey is the idempotency key for operations that are naturally
// singular per customer and provider — asking for a terms page twice should get
// the same page back, not a second one.
func derivedKey(op string, customerID uint, provider string, suffix ...string) string {
	key := fmt.Sprintf("%s:%d:%s", op, customerID, provider)
	for _, part := range suffix {
		key += ":" + part
	}
	return key
}

// normalizeRedirectURI prepares a redirect URI for hashing. Whitespace is
// trimmed and, when parseable, scheme and host are lowercased so cosmetic
// casing differences do not mint new ToS sessions.
func normalizeRedirectURI(uri string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return uri
	}
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme == "" {
		return uri
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Host != "" {
		parsed.Host = strings.ToLower(parsed.Host)
	}
	return parsed.String()
}

func redirectURIHash(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// derivedTosKey scopes the ToS idempotency key to the redirect URI and to the
// UTC day. Bridge rejects a reused key when the body differs (422), which
// happens after the app changes how it returns from the hosted ToS page, and
// it also refuses any key first used more than 24 hours earlier (422
// "Idempotency Key Retry Deadline Exceeded") — a key fixed per customer and
// provider locked the terms step for good for anyone who came back a day
// later. Within one day the key still dedupes concurrent calls into one terms
// page. The redirect is hashed server-side so client-controlled bytes cannot
// inflate the key space.
func derivedTosKey(customerID uint, provider, redirectURI string, at time.Time) string {
	return derivedKey("kyc-tos", customerID, provider,
		redirectURIHash(normalizeRedirectURI(redirectURI)), at.UTC().Format(time.DateOnly))
}

// decodeStrings reads one back. A malformed value yields nil rather than an
// error: these lists are advisory text for the app, and failing a whole
// response over one of them would be the worse outcome.
func decodeStrings(encoded string) []string {
	if encoded == "" {
		return nil
	}
	var list []string
	if err := json.Unmarshal([]byte(encoded), &list); err != nil {
		return nil
	}
	return list
}
