package ozav

import (
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// kycStatusFromOzav narrows a customer status; Ozav's own value survives in
// `raw_status`. `suspended` is paused, not rejected: an operational block that
// can resume. Approval needs more than this (see customerKycStatus).
func kycStatusFromOzav(status string) string {
	switch status {
	// A record that exists and is not approved (Ozav's panel badges it "In
	// review"). not_started would mean the user never touched the provider,
	// and told one holding a customer under review to start again (02/09).
	case "pending":
		return models.KycStatusIncomplete
	case "in_review":
		return models.KycStatusUnderReview
	case "approved":
		return models.KycStatusApproved
	case "rejected":
		return models.KycStatusRejected
	case "suspended":
		return models.KycStatusPaused
	default:
		// Grant no rail and send the user chasing no requirement.
		return models.KycStatusUnderReview
	}
}

// customerKycStatus downgrades an approved customer with no verification.mode
// to under_review: Ozav's docs say an absent mode is an unknown posture, and
// status alone is not proof the customer was vetted.
func customerKycStatus(status, verificationMode string) string {
	mapped := kycStatusFromOzav(status)
	if mapped == models.KycStatusApproved && verificationMode == "" {
		return models.KycStatusUnderReview
	}
	return mapped
}

// customerStatus is customerKycStatus read against the customer's own
// requirements: Ozav keeps a customer `in_review` while it waits on data only
// the user can send. Both customers its compliance chased on 28/09/2026 were
// there — one with seven missing_fields and `owner: ozav` (a human is on the
// case), one with a bank statement in documents_due — and under_review told
// the app there was nothing to do, so it showed a waiting screen for a review
// that could not move. incomplete is what reopens the wizard on those steps.
//
// It reads the requirements rather than `owner`, because owner stays `ozav`
// under human review even with the record incomplete. Approved and rejected
// are left alone: an approved customer carries risk-profile missing_fields
// as a matter of course, and a rejection is final.
//
// `pending` is the other side of the same split: Ozav keeps a customer
// `pending` both while it reviews and while it waits on the integrator, and
// only decision.status tells them apart. `pending_review` with nothing owed
// is a review, and incomplete would send the app looking for a step that
// does not exist. A record with no decision (older than 2026-09-08) keeps
// incomplete.
func customerStatus(c customerResponse) string {
	status := customerKycStatus(c.Status, c.Verification.Mode)
	if c.Status == "in_review" && c.Requirements.waitingOnData() {
		return models.KycStatusIncomplete
	}
	if c.Status == "pending" && c.decisionStatus() == decisionPendingReview && !c.Requirements.waitingOnData() {
		return models.KycStatusUnderReview
	}
	return status
}

// Ozav's decision.status values this adapter reads.
const (
	decisionPendingReview        = "pending_review"
	decisionInformationRequested = "information_requested"
)

func (c customerResponse) decisionStatus() string {
	if c.Decision == nil {
		return ""
	}
	return c.Decision.Status
}

// unnamedRequest reports Ozav waiting on the integrator (owner `integrator`,
// or information_requested) while the customer names nothing to send: no due
// line, no missing field, nothing in stillRequired: information_requested
// with every documents_due line submitted or accepted and none of them
// marked invalid. Nothing the user sends can answer it, so it is logged for
// an operator to take to Ozav.
func (c customerResponse) unnamedRequest(stillRequired []string) bool {
	asked := c.Requirements.Owner == "integrator" || c.decisionStatus() == decisionInformationRequested
	named := c.Requirements.waitingOnData() || len(c.Requirements.Due) > 0 || len(stillRequired) > 0
	return asked && !named && c.Status != "rejected"
}

// logUnnamedRequest warns when unnamedRequest holds.
func (c customerResponse) logUnnamedRequest(stillRequired []string) {
	if !c.unnamedRequest(stillRequired) {
		return
	}
	reason := ""
	if c.Decision != nil {
		reason = c.Decision.ReasonCode
	}
	ramp.LogUnnamedKycRequest(string(ramp.ProviderOzav), c.ID, reason)
}

// transferStatusFromOzav narrows a transfer status. needs_review and anything
// unknown stay processing: never terminal, so a later event can still move
// them, and never completed, which would credit an order that has not settled.
func transferStatusFromOzav(status string) string {
	switch status {
	case "awaiting_payment":
		return models.TxStatusAwaitingFunds
	case "payment_confirmed":
		return models.TxStatusFundsReceived
	case "converting", "payout_initiated", "needs_review":
		return models.TxStatusProcessing
	case "completed":
		return models.TxStatusCompleted
	case "failed", "cancelled":
		return models.TxStatusFailed
	case "expired":
		return models.TxStatusExpired
	default:
		return models.TxStatusProcessing
	}
}
