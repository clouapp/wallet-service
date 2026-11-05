package bridge

import (
	"slices"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// bridgeStatusOffboarded is the one customer state compared outside the table:
// Bridge ending the relationship.
const bridgeStatusOffboarded = "offboarded"

// bridgeNotAvailableInRegion is Bridge ruling an endorsement out where the
// customer lives: the state its BRL migration guide gives pix_onramp for
// anyone outside Brazil, which its endorsement guide shows as an issue on an
// otherwise ordinary entry. Both spellings are read.
const bridgeNotAvailableInRegion = "endorsement_not_available_in_customers_region"

// The customer states in which Bridge waits on the customer for something
// beyond a document: the questionnaire, and a business's owners.
const (
	bridgeStatusAwaitingQuestionnaire = "awaiting_questionnaire"
	bridgeStatusAwaitingUBO           = "awaiting_ubo"
)

// requirementCPFTaxID is what the sandbox gave a passport holder's pix_onramp
// instead of that state (measured 07/10/2026): a CPF the person does not
// have. No step of the form supplies one, so it is not something they owe.
const requirementCPFTaxID = "has_tin_of_type_cpf"

// kycStatusFromBridge narrows a customer state; the provider's own value
// survives in `raw_status`. awaiting_questionnaire and awaiting_ubo mean Bridge
// needs more from the customer; offboarded is terminal, so rejected rather than
// paused, which would suggest the account can resume.
func kycStatusFromBridge(status string) string {
	switch status {
	case "active":
		return models.KycStatusApproved
	case "not_started":
		return models.KycStatusNotStarted
	case "incomplete":
		return models.KycStatusIncomplete
	case "under_review":
		return models.KycStatusUnderReview
	case "rejected", bridgeStatusOffboarded:
		return models.KycStatusRejected
	case "paused":
		return models.KycStatusPaused
	case bridgeStatusAwaitingQuestionnaire, bridgeStatusAwaitingUBO:
		return models.KycStatusRequiresInfo
	default:
		// Something is happening upstream that we cannot name: grant no rail
		// and send the user chasing no requirement.
		return models.KycStatusUnderReview
	}
}

// transferStatusFromBridge narrows a transfer state. `returned` is refunded,
// not failed: the money came back and the user is whole.
func transferStatusFromBridge(state string) string {
	switch state {
	case "awaiting_funds":
		return models.TxStatusAwaitingFunds
	case "funds_received":
		return models.TxStatusFundsReceived
	case "in_review", "payment_submitted":
		return models.TxStatusProcessing
	case "payment_processed":
		return models.TxStatusCompleted
	case "returned", "refunded":
		return models.TxStatusRefunded
	case "canceled", "error", "undeliverable":
		return models.TxStatusFailed
	default:
		// Never terminal, or no later event could move the order; never
		// completed, or it would credit an order that is not.
		return models.TxStatusProcessing
	}
}

// depositStatusFromBridge maps a virtual account activity type, read from
// `event_object.type` (see DecodeWebhook). A microdeposit never becomes a
// transaction, so it is classified out before this. Bridge's webhook examples
// spell two of the types differently from its events page — `funding` for
// funds_received, `refund` for refunded (webhooks/structure, 2026-10-14) — so
// both spellings are read.
func depositStatusFromBridge(activityType string) string {
	switch activityType {
	case "funds_received", "funds_scheduled", "funding":
		return models.TxStatusFundsReceived
	case "in_review", "payment_submitted", "refund_in_flight":
		// refund_in_flight is not `refunded`: the money is still in transit.
		return models.TxStatusProcessing
	case "payment_processed":
		return models.TxStatusCompleted
	case "refunded", "refund":
		return models.TxStatusRefunded
	case "refund_failed":
		// The money is stuck and needs support; `refunded` would say the user
		// is whole.
		return models.TxStatusFailed
	default:
		// Same rule as transferStatusFromBridge.
		return models.TxStatusProcessing
	}
}

// endorsementStatusFromBridge falls back to incomplete: the router grants a
// rail only on `approved`, so the unknown case has to be anything else.
func endorsementStatusFromBridge(status string) string {
	switch status {
	case models.EndorsementStatusApproved,
		models.EndorsementStatusPending,
		models.EndorsementStatusIncomplete,
		models.EndorsementStatusRevoked:
		return status
	case bridgeNotAvailableInRegion:
		return models.EndorsementStatusNotAvailable
	default:
		return models.EndorsementStatusIncomplete
	}
}

func tosStatusFromBridge(accepted bool) string {
	if accepted {
		return models.TosStatusApproved
	}
	return models.TosStatusPending
}

// flatRequirements lists issues first (something already wrong), then what is
// pending, then what was never supplied.
func (e endorsementResponse) flatRequirements() []string {
	var out []string
	out = append(out, e.Requirements.Issues...)
	out = append(out, e.Requirements.Pending...)
	if e.Requirements.Missing != nil {
		out = append(out, e.Requirements.Missing.AllOf...)
		out = append(out, e.Requirements.Missing.AnyOf...)
	}
	return out
}

// notAvailableInRegion reports whether Bridge has ruled this endorsement out
// for the customer's region, by its status or by an issue — read as the
// rejection reads issues (normalizedIssue).
func (e endorsementResponse) notAvailableInRegion() bool {
	return e.Status == bridgeNotAvailableInRegion ||
		slices.ContainsFunc(e.Requirements.Issues, func(issue string) bool {
			return normalizedIssue(issue) == bridgeNotAvailableInRegion
		})
}

// owedRequirements is what this endorsement asks of the user: nothing once
// the region rules it out, and from pix_onramp never the CPF a passport
// holder does not have. The endorsement keeps its own list; this one feeds
// the customer's, which is what becomes wizard steps.
func (e endorsementResponse) owedRequirements() []string {
	if e.notAvailableInRegion() {
		return nil
	}
	items := e.flatRequirements()
	if e.Name != models.EndorsementPixOnramp {
		return items
	}
	return slices.DeleteFunc(items, func(item string) bool { return item == requirementCPFTaxID })
}

// endorsements maps each endorsement. One the region rules out is final and
// keeps no requirement: nothing the user sends would change it.
func (r customerResponse) endorsements() []models.Endorsement {
	if len(r.Endorsements) == 0 {
		return nil
	}
	out := make([]models.Endorsement, 0, len(r.Endorsements))
	for _, e := range r.Endorsements {
		if e.notAvailableInRegion() {
			out = append(out, models.Endorsement{Name: e.Name, Status: models.EndorsementStatusNotAvailable})
			continue
		}
		out = append(out, models.Endorsement{
			Name:         e.Name,
			Status:       endorsementStatusFromBridge(e.Status),
			Requirements: e.flatRequirements(),
		})
	}
	return out
}

// requirements is everything the user still owes, from the customer and from
// each endorsement (owedRequirements). Bridge repeats items across both and
// the list is rendered as-is, so it is deduplicated in first-seen order.
//
// A business Bridge holds at awaiting_ubo or awaiting_questionnaire owes that
// state first, named as the state: Bridge may name no code beside it, and a
// business owing its owners or its questionnaire that named nothing read as
// in review forever (the KYC service maps both onto PJ steps). A person's
// state is left as it reads.
func (r customerResponse) requirements() []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(items []string) {
		for _, item := range items {
			if item == "" {
				continue
			}
			if _, dup := seen[item]; dup {
				continue
			}
			seen[item] = struct{}{}
			out = append(out, item)
		}
	}
	if r.Type == customerTypeBusiness && (r.Status == bridgeStatusAwaitingUBO || r.Status == bridgeStatusAwaitingQuestionnaire) {
		add([]string{r.Status})
	}
	add(r.RequirementsDue)
	for _, e := range r.Endorsements {
		add(e.owedRequirements())
	}
	return out
}

// rejectionMessages returns only the user-facing reasons: `developer_reason`
// names vendor codes and internal checks.
func (r customerResponse) rejectionMessages() []string {
	if len(r.RejectionReasons) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.RejectionReasons))
	for _, reason := range r.RejectionReasons {
		if reason.Reason != "" {
			out = append(out, reason.Reason)
		}
	}
	return out
}
