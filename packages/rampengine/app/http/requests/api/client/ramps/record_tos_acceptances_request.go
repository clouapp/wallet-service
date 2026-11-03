package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// RecordTosAcceptancesRequest validates POST /api/kyc/tos-acceptances.
//
// A list rather than a single pair because the terms step can have more than
// one provider pending, and the client reports each acceptance as it happens.
type RecordTosAcceptancesRequest struct {
	requests.Open
	Acceptances []TosAcceptanceInput `form:"acceptances" json:"acceptances"`
}

// TosAcceptanceInput is one provider's signed agreement.
//
// The client naming a provider here is not the client choosing one: it is the
// name the server returned from tos-sessions, travelling back with the session
// it belongs to. The agreement means nothing at any other provider.
type TosAcceptanceInput struct {
	Provider string `form:"provider" json:"provider"`
	// SignedAgreementID is what a provider's own page handed the client. An
	// attesting provider issues none — the client never sees its session
	// token — so exactly one of this and LegalName is expected per entry.
	SignedAgreementID string `form:"signed_agreement_id" json:"signed_agreement_id"`
	// LegalName is who accepted, for an attesting provider. It comes from the
	// client because nothing on this side holds it yet: `users` has no name
	// column and the application carrying one has not been submitted. It must
	// be the same string that application later sends.
	LegalName string `form:"legal_name" json:"legal_name"`
}

// Rules bounds the envelope, each entry and the provider codes inside it.
//
// ProviderRule is the shared constant that exists so this is not a second copy
// of the vocabulary. Leaving the codes to the service meant a typo travelled as
// far as factory.Get, came back as ErrProviderNotImplemented and reached the
// client as 503 no_provider_available — the code that means "the rail is down,
// try later" — for a request that will never succeed.
func (r *RecordTosAcceptancesRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{
		// One entry per provider: each costs database work the request timeout does not stop.
		"acceptances": "required|max_len:4",
		// Each entry carries a signed_agreement_id or a legal_name; see
		// rules.TosAcceptanceRule for why that is not required_without.
		"acceptances.*":          "tos_acceptance",
		"acceptances.*.provider": ProviderRule,
		// Bounded to the columns that will hold them, so a value accepted
		// here cannot be refused there.
		"acceptances.*.signed_agreement_id": "max_len:64",
		"acceptances.*.legal_name":          "max_len:128",
	}
}
