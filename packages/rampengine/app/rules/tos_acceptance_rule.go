package rules

import (
	"context"

	"github.com/goravel/framework/contracts/validation"
)

// TosAcceptanceRule accepts one entry of a terms acceptance batch when it
// carries something to record: a signed_agreement_id or a legal_name.
//
// A hosted provider's page hands the client an agreement id; an attesting
// provider hands it nothing, and the user's name is what stands in its place.
// Which of the two applies is the service's to decide — it knows the adapter —
// so the check here is only that the entry carries one of them.
//
// It is a rule on the entry rather than required_without on either field:
// over a wildcard, required_without asks whether any entry in the list
// carries the other field, so one entry's legal_name would wave through a
// sibling carrying neither.
type TosAcceptanceRule struct{}

func (r *TosAcceptanceRule) Signature() string { return "tos_acceptance" }

func (r *TosAcceptanceRule) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	entry, ok := val.(map[string]any)
	if !ok {
		return false
	}
	// Anything but null or "" binds to a non-empty string on the request.
	agreement, name := entry["signed_agreement_id"], entry["legal_name"]
	return (agreement != nil && agreement != "") || (name != nil && name != "")
}

func (r *TosAcceptanceRule) Message(_ context.Context) string {
	return "each acceptance must carry a signed_agreement_id or a legal_name"
}
