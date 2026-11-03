package ramp

import (
	"errors"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// DefaultErrorCode is what an error nobody recognises maps to: a generic
// answer rather than leaking whatever the upstream call said.
const DefaultErrorCode = "provider_unavailable"

// ErrorCode maps a ramp sentinel to the stable code the client contract
// exposes for it, or "" when the error is not one of this package's
// sentinels — never the upstream message, which can restate customer data.
//
// It lives here, at the bottom of the import graph, because two readers need
// it: the HTTP layer, which turns a service error into a response code, and
// the KYC service, which records why a queued submission failed so the row can
// say so long after the request that queued it has returned. The service
// cannot import the HTTP layer, and carrying a second copy of this table there
// is how the two would come to disagree.
//
// The table is deliberately incomplete. ErrProviderCredentialsInvalid,
// ErrIdempotencyConflict and ErrUploadHostNotAllowed have no case: each is a
// fault on XIP's side — a key to rotate, a collision we generated, a host we
// refused to trust — that the user cannot act on and that would leak internal
// state if named. They resolve to "", which every caller reads as the default.
//
// They share the default code but not the status: the first two only reach a
// client inside an adapter's *UpstreamError, which is ErrProviderFailed
// whatever it wraps, so they are 502; the third is produced locally and
// answers 500.
func ErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrIntegrationRequired):
		return "integration_required"
	case errors.Is(err, ErrKycPending):
		return "kyc_pending"
	// kyc_pending's 409 under its own code: see ErrKycRejected.
	case errors.Is(err, ErrKycRejected):
		return "kyc_rejected"
	// The same 409, and no provider to name: see ErrRailNotEligible.
	case errors.Is(err, ErrRailNotEligible):
		return "rail_not_eligible"
	case errors.Is(err, ErrTosRequired):
		return "tos_required"
	case errors.Is(err, ErrTosNotRequired):
		return "tos_not_required"
	case errors.Is(err, ErrTosSessionUnknown):
		return "tos_session_unknown"
	case errors.Is(err, ErrTosSessionExpired):
		return "tos_session_expired"
	case errors.Is(err, ErrTosLegalNameRequired):
		return "tos_legal_name_required"
	case errors.Is(err, ErrMfaStepUpRequired):
		return "mfa_required"
	case errors.Is(err, ErrEstimateExpired):
		return "estimate_expired"
	case errors.Is(err, ErrQuoteExpiredSettlement):
		return "quote_expired_settlement"
	case errors.Is(err, ErrEstimateConsumed):
		return "estimate_consumed"
	case errors.Is(err, ErrEstimateMismatch):
		return "estimate_mismatch"
	case errors.Is(err, ErrSandboxOnly):
		return "sandbox_only"
	case errors.Is(err, ErrEstimateRequired):
		return "estimate_required"
	case errors.Is(err, ErrAmountBelowMinimum):
		return "amount_below_minimum"
	case errors.Is(err, ErrAmountAboveMaximum):
		return "amount_above_maximum"
	case errors.Is(err, ErrInvalidWallet):
		return "invalid_wallet"
	// no_provider_available's status under its own code: see ErrRailDisabled.
	case errors.Is(err, ErrRailDisabled):
		return "rail_disabled"
	case errors.Is(err, ErrNoProviderAvailable), errors.Is(err, ErrProviderNotImplemented),
		errors.Is(err, ErrProviderFeatureDisabled):
		return "no_provider_available"
	// Its own code because the instruction is the opposite of every other
	// failure here: try the same call again. Folded into the default, the app
	// would give up on a provider that is merely throttled. An unprovisioned
	// Ozav payout destination is retryable too: see ErrOzavPayoutNotProvisioned.
	case errors.Is(err, ErrRetryableUpstream), errors.Is(err, ErrOzavPayoutNotProvisioned):
		return "provider_busy"
	// Named so the app can fall back to the in-app document flow instead of
	// navigating to an empty link.
	case errors.Is(err, ErrHostedLinkUnavailable):
		return "hosted_link_unavailable"
	// Not folded into the default: provider_unavailable blames the provider for
	// an outage that is not happening — the provider is fine, the ask is the
	// problem.
	case errors.Is(err, ErrVerificationFreezesCustomer):
		return "verification_not_supported"
	case errors.Is(err, ErrDocumentsNotSupported):
		return "documents_not_supported"
	// A 422 of its own: the provider would refuse what the user sent, and
	// only the user can change it. Folded into the default it read as an
	// outage the app would retry against.
	case errors.Is(err, ErrKycDataInvalid):
		return "kyc_data_invalid"
	case errors.Is(err, ErrEmailInUse):
		return "email_in_use"
	// The rejection category of the same name: the app already says it and
	// reopens the document step for it (API contract §5.5).
	case errors.Is(err, ErrDocumentRefused):
		return models.KycRejectionDocumentUnreadable
	// A registered bank account the provider has not approved yet: the app
	// waits and asks again, nothing for the user to change.
	case errors.Is(err, ErrBankAccountVerifying):
		return "bank_account_verifying"
	// A bank account the provider refused: the user corrects the beneficiary.
	case errors.Is(err, ErrBankAccountRejected):
		return "bank_account_rejected"
	// Additive codes of the prepared funding: the signed action is refused,
	// the prepared action is stale, or the beneficiary moved under the quote.
	case errors.Is(err, ErrInvalidSignedActionPayload):
		return "invalid_signature"
	case errors.Is(err, ErrFundingActionExpired):
		return "funding_action_expired"
	case errors.Is(err, ErrBeneficiaryChanged):
		return "beneficiary_changed"
	// The finalize's key refusals: a key replayed with another body, and a
	// key that is missing or malformed. Both are the client's to fix.
	case errors.Is(err, ErrIdempotencyKeyReused):
		return "idempotency_key_reused"
	case errors.Is(err, ErrInvalidIdempotencyKey):
		return "idempotency_key_required"
	default:
		return ""
	}
}
