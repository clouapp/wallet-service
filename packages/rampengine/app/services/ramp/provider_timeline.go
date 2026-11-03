package ramp

import (
	"errors"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// Operations a timeline entry can describe. They are this side's vocabulary,
// not any provider's: the same act is a different path at each provider, and
// what a reader compares is the act.
const (
	OpSubmitApplication = "submit_application"
	OpSubmitDocuments   = "submit_documents"
	// OpStartVerification has no writer left: opening a verification is what
	// froze an Ozav customer, and the call was removed on 03/09/2026 (see
	// adapters/ramp/ozav/capabilities.go). The name stays because rows
	// written before then still carry it, and a timeline that cannot name its
	// own history is worse than one carrying a retired verb.
	OpStartVerification  = "start_verification"
	OpRefreshKyc         = "refresh_kyc"
	OpStartTos           = "start_tos"
	OpAcceptTos          = "accept_tos"
	OpAdoptCustomer      = "adopt_customer"
	OpRequestEndorsement = "request_endorsements"
	OpHostedLink         = "hosted_link"
	OpRelinkCustomer     = "relink_customer"
)

// ProviderTimeline records what XIP asked a provider about one user, and what
// came back — including the times it decided not to ask.
//
// The user is a parameter and never comes off the context. That is the whole
// design: the layer that knows which user this is about is the layer that
// calls this, and a context value would be a dependency that cannot fail
// loudly — the one `context.Background()` nobody noticed would silently
// produce a timeline missing exactly the calls made from there.
//
// It records metadata only. See models.ProviderCall.
type ProviderTimeline struct {
	repo repositories.ProviderCallRepository
}

func NewProviderTimeline(repo repositories.ProviderCallRepository) *ProviderTimeline {
	return &ProviderTimeline{repo: repo}
}

// Entry is one line, as the caller knows it: UserID made the call for
// CustomerID — the user's own customer, or a company they act for.
type Entry struct {
	UserID       uint
	CustomerID   uint
	Provider     string
	Operation    string
	SubmissionID string
	// Started, when set, is what latency is measured from.
	Started time.Time
	// Err is the call's outcome. Nil records a success.
	Err error
	// SkipReason turns the entry into a decision not to call. It is the reason
	// in this side's words — "nothing owed", "no application in flight" — and
	// it is what makes an absence readable instead of merely absent.
	SkipReason string
}

// Record writes one entry.
//
// It never returns an error and never fails the operation it describes. An
// audit line that can refuse a submission would make the timeline a new way
// for a KYC to fail, which is a worse trade than a timeline with a hole — and
// a hole here is visible, because the reader is already looking for gaps.
//
// It is deliberately not wrapped in the caller's transaction either. A refused
// submission rolls its own row write back, and the refusal is precisely what
// has to survive that rollback: the sync terms refusal on 02/09 left no row,
// no event and no trace, which is the failure this exists to end.
func (t *ProviderTimeline) Record(entry Entry) {
	call := &models.ProviderCall{
		UserID:     entry.UserID,
		CustomerID: entry.CustomerID,
		Provider:   entry.Provider,
		Operation:  entry.Operation,
		Outcome:    outcomeOf(entry),
	}
	if entry.SubmissionID != "" {
		id := entry.SubmissionID
		call.SubmissionID = &id
	}
	if code := codeOf(entry); code != "" {
		call.ErrorCode = &code
	}
	if !entry.Started.IsZero() {
		ms := uint(time.Since(entry.Started).Milliseconds()) //nolint:gosec // a latency cannot be negative
		call.LatencyMs = &ms
	}
	if id := providerRequestID(entry.Err); id != "" {
		call.RequestID = &id
	}

	if err := t.repo.Create(call); err != nil {
		// Logged and dropped. The alternative is failing a submission over its
		// own audit line, and the operator reading a gap is better served by a
		// gap than by a KYC that could not be sent.
		facades.Log().Errorf("provider timeline: record %s at %s for user %d: %v",
			entry.Operation, entry.Provider, entry.UserID, err)
	}
}

func outcomeOf(entry Entry) string {
	switch {
	case entry.SkipReason != "":
		return models.ProviderCallSkipped
	case entry.Err != nil:
		return models.ProviderCallFailed
	default:
		return models.ProviderCallOK
	}
}

// codeOf is the stable contract code, never the upstream text. A provider's
// message restates submitted fields often enough that it cannot sit next to a
// user id, which is the rule this table inherits from telemetry.go.
func codeOf(entry Entry) string {
	if entry.SkipReason != "" {
		return entry.SkipReason
	}
	if entry.Err == nil {
		return ""
	}
	// A refusal of the applicant is the provider's answer, not an outage: the
	// default below would file it as provider_unavailable. It takes the code
	// the client contract gives a provider done with the user.
	var refusal *ramp.KycRefusal
	if errors.As(entry.Err, &refusal) {
		return refusalCode
	}
	if code := ramp.ErrorCode(entry.Err); code != "" {
		return code
	}
	return ramp.DefaultErrorCode
}

// refusalCode is ramp.ErrorCode's name for ErrKycRejected.
var refusalCode = ramp.ErrorCode(ramp.ErrKycRejected)

// providerRequestID digs out the provider's own identifier for the call. It is
// the first thing their support asks for, and recovering one previously meant
// reading server logs that may already have been pruned.
func providerRequestID(err error) string {
	if err == nil {
		return ""
	}
	var carrier interface{ ProviderRequestID() string }
	if errors.As(err, &carrier) {
		return carrier.ProviderRequestID()
	}
	return ""
}
