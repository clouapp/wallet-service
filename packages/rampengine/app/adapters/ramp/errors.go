package ramp

import (
	"errors"
	"fmt"
)

// Sentinel errors for the multi-provider ramp stack. Services map these to the
// stable client error codes documented in the design spec, so no caller has to
// parse an error string.
var (
	// ErrProviderNotImplemented is returned by the MutualPay/Ozav stubs for
	// every operation that has no adapter behind it yet. The router skips
	// stubs before calling them; this covers a direct `Factory.Get` caller.
	ErrProviderNotImplemented = errors.New("ramp: provider not implemented")
	// ErrNoProviderAvailable means the rail has an order configured but no
	// entry in it survived eligibility (disabled, stub, or not configured).
	ErrNoProviderAvailable = errors.New("ramp: no provider available")
	// ErrRailDisabled means an operator switched this rail off — product-wide
	// or for this user. It is deliberately distinct from
	// ErrNoProviderAvailable: both produce the same screen, but one is a
	// decision and the other is a configuration gap, and whoever reads the logs
	// at 3am has to be able to tell them apart.
	ErrRailDisabled = errors.New("ramp: rail disabled")
	// ErrInvalidDestination is an off-ramp request whose destination does not
	// fit its rail: a bank rail with no registered account or with a Pix key,
	// or a Pix rail carrying a bank account. It is a programming error on our
	// side, never something the user typed, so it maps to a 500, not a 422.
	ErrInvalidDestination = errors.New("ramp: destination does not match the rail")
	// ErrInvalidWallet is a wallet address the user typed that is not an
	// address on its chain, refused before any provider is called. Unlike
	// ErrInvalidDestination it is the user's to fix, so it maps to a 422.
	ErrInvalidWallet = errors.New("ramp: wallet address is not valid on its chain")
	// ErrIntegrationRequired means the user has no active integration with any
	// otherwise-eligible provider — the client must start one first.
	ErrIntegrationRequired = errors.New("ramp: integration required")
	// ErrKycPending means the integration exists but its endorsements do not
	// cover the requested rail yet.
	ErrKycPending = errors.New("ramp: kyc pending")
	// ErrBankAccountVerifying is a registered bank account the provider has
	// not approved yet; a quote against it waits.
	ErrBankAccountVerifying = errors.New("ramp: bank account still verifying")
	// ErrBankAccountRejected is a bank account the provider refused; the
	// customer has to correct the beneficiary.
	ErrBankAccountRejected = errors.New("ramp: provider rejected the bank account")
	// ErrKycRejected is a provider that refused this customer for good. It is
	// separate from ErrKycPending because the two ask opposite things of the
	// client: pending opens the wizard for that provider, rejected must not —
	// a wizard over a decision nothing the user types can change is a loop
	// that fails identically every time.
	ErrKycRejected = errors.New("ramp: provider refused this customer")
	// ErrRailNotEligible is a rail no provider in the order will open to this
	// person: the tax identity and residence they declared do not open it
	// there (RailsOf), or the provider approved the customer and said the
	// rail is not available where they live. It names no provider, because
	// no wizard step changes the answer — a different declaration might.
	ErrRailNotEligible = errors.New("ramp: rail not eligible for this person")
	// ErrEstimateExpired is a create referencing an estimate past its
	// `expires_at`. The client must re-estimate.
	ErrEstimateExpired = errors.New("ramp: estimate expired")
	// ErrQuoteExpiredSettlement is a payout attempted after the upstream quote
	// expired while the user's crypto is already at the provider. The client
	// must not re-estimate blindly — support re-quote is required.
	ErrQuoteExpiredSettlement = errors.New("ramp: quote expired after deposit")
	// ErrEstimateRequired is a create with no estimate on a provider whose
	// quotes are binding — the order cannot be priced without one.
	ErrEstimateRequired = errors.New("ramp: estimate required")
	// ErrEstimateConsumed is a create referencing an estimate that already
	// produced an order. Binding quotes are single-use.
	ErrEstimateConsumed = errors.New("ramp: estimate already used")
	// ErrEstimateMismatch is a create whose body diverges from the persisted
	// estimate snapshot — the client must re-estimate.
	ErrEstimateMismatch = errors.New("ramp: estimate mismatch")
	// ErrAmountBelowMinimum / ErrAmountAboveMaximum are the rail's configured
	// limits in `rail_routing_settings`, checked before any upstream call.
	ErrAmountBelowMinimum = errors.New("ramp: amount below minimum")
	ErrAmountAboveMaximum = errors.New("ramp: amount above maximum")
	// ErrDocumentsNotSupported is a document submission against a provider
	// whose documents API is not available.
	ErrDocumentsNotSupported = errors.New("ramp: documents not supported")
	// ErrKycDataInvalid is an application or a document set that lacks, or
	// misspells, something the provider's customer create needs, found by the
	// adapter before any call. It is a 422 the user fixes by correcting the
	// field — unlike ErrDocumentsNotSupported, a 409 no input changes. The
	// message names the field, never its value.
	ErrKycDataInvalid = errors.New("ramp: kyc data does not meet the provider's requirements")
	// ErrMfaStepUpRequired is an off-ramp create by a user with MFA enabled who
	// sent no verification code.
	ErrMfaStepUpRequired = errors.New("ramp: mfa step-up required")
	// ErrTosRequired is an application submitted before the provider's terms of
	// service were signed.
	ErrTosRequired = errors.New("ramp: terms of service not signed")
	// ErrTosNotRequired is a terms-of-service session asked of a provider that
	// has none. It is distinct from "not implemented": the caller should carry
	// on rather than abort, because there is no step to complete.
	ErrTosNotRequired = errors.New("ramp: provider requires no terms of service")

	// ErrTosSessionUnknown is an acceptance for terms this side never opened.
	// The agreement id is the provider's word, relayed by the client, and
	// nothing about it can be checked here — so what is checked is that a
	// session was issued at all. Without that, a stale id, a truncated
	// postMessage or a typo would be recorded as a signature.
	ErrTosSessionUnknown = errors.New("ramp: no terms session was issued for this provider")
	// ErrTosSessionExpired is a terms session the provider will no longer
	// honour. It is separate from ErrTosSessionUnknown only in what it tells an
	// operator reading the row — both mean the same thing to the flow: whatever
	// this side holds is dead, and the terms step has to run again from a new
	// session. Neither may be treated as ErrTosRequired, which says the user
	// never signed and would send the client back to a session it already has.
	ErrTosSessionExpired = errors.New("ramp: terms session expired")
	// ErrTosLegalNameRequired is an attestation submitted without the name of
	// the person who accepted.
	//
	// A provider that lets us collect the acceptance ourselves takes our word
	// for who gave it, and the name is the whole of that word. Refusing here
	// rather than forwarding an empty one keeps us from attesting on behalf of
	// nobody.
	ErrTosLegalNameRequired = errors.New("ramp: terms attestation needs the legal name of who accepted")
	// ErrStandingAccountUnsupported means the provider does not collect through a
	// standing deposit account — it has no VirtualAccountAdapter behind it.
	//
	// It is a routing signal, not a failure: the provider was never asked to do
	// anything, so the next one in the rail order may still serve. It is the only
	// error FailoverOnUnsupported advances on, precisely because nothing upstream
	// happened.
	ErrStandingAccountUnsupported = errors.New("ramp: provider does not collect through a standing account")
	// ErrRequestNotSent marks a failure the adapter can *prove* happened before
	// the request reached the provider — connection refused, DNS failure, a
	// throttle rejected with no body, or a step a create fails before its
	// money-moving request (NotSent).
	//
	// It is what makes failover safe on a create. A timeout, a truncated
	// response or any 5xx once a connection existed is unknown state: retrying
	// that on another provider is how one payment becomes two (ramp.Send draws
	// that line).
	ErrRequestNotSent = errors.New("ramp: request not sent")
	// ErrAtomicMaterialInvalid is an atomic create's stored material — an
	// upload checkpoint, a prepared body — that does not belong to the call it
	// was handed to: another version, another submission, a body whose digest
	// no longer matches. It is refused before anything is sent, like
	// ErrRequestNotSent, but it is deterministic: the same material is refused
	// the same way on every retry, so it is never transient.
	ErrAtomicMaterialInvalid = errors.New("ramp: atomic create material does not match")
	// ErrProviderFailed marks a call the provider did not answer usefully: it
	// was unreachable, the connection broke, or it answered anything but a 2xx
	// this side could decode. Only the HTTP clients attach it, and it is the one
	// thing the HTTP layer answers 502 for — every other failure is ours, and a
	// 502 would hide it from whoever is looking for 500s.
	//
	// It says nothing about whether the request was processed; that is
	// ErrRequestNotSent's job, and a failure can carry both.
	ErrProviderFailed = errors.New("ramp: provider call failed")
	// ErrSandboxOnly marks an operation that exists only when the provider runs in sandbox.
	ErrSandboxOnly = errors.New("ramp: sandbox only")
	// ErrOfframpCreateUnavailable marks a provider whose off-ramp quote is
	// implemented but whose create is not. Ozav is the first case: a
	// wallet-sourced payout to a Pix destination answers 422
	// PROVISIONING_REQUIRED by construction, measured against the sandbox,
	// and this sentinel keeps that scoping decision distinct from
	// ErrProviderNotImplemented — the quote side works.
	ErrOfframpCreateUnavailable = errors.New("ramp: offramp create unavailable")
	// ErrProviderCredentialsInvalid is a provider refusing the key itself.
	// It is separated from every other rejection because it is the one an
	// admin can actually fix, and fix without a deploy: rotate the key in
	// settings. Folding it into a generic upstream failure would send whoever
	// is on call reading application logs for a configuration problem.
	ErrProviderCredentialsInvalid = errors.New("ramp: provider credentials invalid")
	// ErrProviderFeatureDisabled is a provider refusing a rail or a feature
	// the XIP account is not enabled for (Bridge's 401 not_allowed). It is not
	// a key to rotate: the same key works everywhere else, and reading it as
	// ErrProviderCredentialsInvalid sent whoever was on call to rotate a key
	// that was fine. The client hears no_provider_available.
	ErrProviderFeatureDisabled = errors.New("ramp: provider feature not enabled")
	// ErrIdempotencyConflict is the same idempotency key replayed with a
	// different body — a collision on XIP's side, never the user's.
	//
	// It must not be confused with ErrEstimateMismatch, whose meaning is "the
	// body disagreed with the estimate's snapshot" and whose client-facing
	// instruction is to re-quote. Re-quoting fixes nothing here: the key is
	// derived from a submission id, so the same call would collide again.
	ErrIdempotencyConflict = errors.New("ramp: idempotency key reused with a different body")
	// ErrOzavPayoutNotProvisioned is Ozav refusing a payout destination that
	// is not enabled for transfers. Named for the provider on purpose: it is
	// not a shape any other provider has.
	//
	// It is TRANSIENT, on Ozav's own instruction. Its PayoutDestination schema
	// states that a destination is `active` the moment it is created but stays
	// unusable until registration with the settlement rail completes, that no
	// field distinguishes the two states, and that an integrator must "treat
	// 422 PROVISIONING_REQUIRED as retryable rather than as a bad request".
	ErrOzavPayoutNotProvisioned = errors.New("ramp: ozav payout destination not provisioned")
	// ErrHostedLinkUnavailable is a provider that accepted the request for a
	// hosted verification session and returned no URL. It exists so the
	// adapter can fail loudly instead of handing an empty string up to be
	// persisted and served to the app as a link.
	ErrHostedLinkUnavailable = errors.New("ramp: provider issued no hosted verification url")
	// ErrVerificationFreezesCustomer is a provider that verifies from the
	// customer record itself, refusing a request to open a separate
	// verification session because opening one stops the verification.
	//
	// Measured on Ozav on 03/09/2026 across every customer in the live
	// organization, 23 of 23 with no exception: the 20 with no verification
	// were approved, and the 3 with one sit at `pending` with their checks at
	// `not_started` — identity documents uploaded and accepted, and no
	// movement in eighteen hours. The 20 include accounts with documents and
	// accounts without, so the documents are not the difference; the
	// verification is.
	//
	// The correct flow for such a provider is to create the customer with an
	// accepted terms session, upload the documents against it, and wait.
	ErrVerificationFreezesCustomer = errors.New("ramp: provider verifies from the customer; opening a verification freezes it")
	// ErrUploadHostNotAllowed is an upload URL pointing outside the hosts the
	// adapter is willing to send its secret key to. The URL arrives in a
	// response body, and the upload has to carry the API key, so an
	// unchecked host is a credential exfiltration primitive handed to
	// whoever can shape that response.
	ErrUploadHostNotAllowed = errors.New("ramp: provider upload url points outside the allow-list")
	// ErrRetryableUpstream is a provider that is temporarily unable to
	// answer — throttled, degraded, or explicitly disabled — where the same
	// request is expected to work later.
	//
	// It deliberately does NOT imply ErrRequestNotSent: the request did reach
	// the provider, so a create carrying it must not be replayed against
	// another provider. Retry means the same request to the same provider.
	ErrRetryableUpstream = errors.New("ramp: provider busy, retry the same request")

	// ErrEmailInUse is a business application whose company e-mail the
	// provider already holds for another customer. It is never adopted
	// (KYB spec §8): the e-mail of a company is no proof the customer found
	// is that company. The user changes the e-mail.
	ErrEmailInUse = errors.New("ramp: the provider holds another customer under this e-mail")

	// ErrDocumentRefused is a document the provider refused as it was sent:
	// a field it cannot take, an image it will not read. Sending the same
	// files again is refused again, so it is no outage and no retry; the user
	// sends the document again (document_unreadable), and the provider's own
	// account of the field goes to the admin.
	ErrDocumentRefused = errors.New("ramp: the provider refused a document")

	// ErrInvalidSignedActionPayload is a signed funding action that does not
	// match what was prepared: another message, another signer, malformed. It
	// is the only business refusal of a validation; any other error is
	// retryable.
	ErrInvalidSignedActionPayload = errors.New("ramp: signed action does not match the prepared one")
	// ErrFundingActionExpired is a prepared funding action past its expiry;
	// the client must prepare again.
	ErrFundingActionExpired = errors.New("ramp: funding action expired")
	// ErrBeneficiaryChanged is a bank account whose beneficiary changed after
	// the quote was taken; the client must quote again.
	ErrBeneficiaryChanged = errors.New("ramp: beneficiary changed since the quote")

	// ErrQuoteAlreadyUsed is a provider refusing to redeem a quote that
	// already produced a payout. It is ambiguous, not a refusal: the payout
	// may be ours, so the finalize outcome is unknown until the recovery finds
	// it or proves it absent.
	ErrQuoteAlreadyUsed = errors.New("ramp: quote already used")
	// ErrBindingMismatch is a provider payout that is not what the recorded
	// binding says (another quote, account, customer or amount). Nothing is
	// applied from it.
	ErrBindingMismatch = errors.New("ramp: payout does not match its binding")
	// ErrInvalidIdempotencyKey is a finalize whose Idempotency-Key is missing
	// or malformed. It is refused before anything is sent.
	ErrInvalidIdempotencyKey = errors.New("ramp: idempotency key missing or invalid")
	// ErrIdempotencyKeyReused is a finalize replaying a key with a different
	// body. The key is the client's to change; nothing is sent.
	ErrIdempotencyKeyReused = errors.New("ramp: idempotency key reused with a different body")
)

// FinalizeRefusedError is a provider refusing a payout finalize with a
// definitive, recognized 4xx: nothing was created, and the refusal is final.
// Code is the provider's own machine code. It is ErrProviderFailed too, so a
// caller that only knows that sentinel still reads it as a failed call; the
// finalize service matches it with errors.As and treats any other failure as
// ambiguous.
type FinalizeRefusedError struct {
	Code string
}

func (e *FinalizeRefusedError) Error() string {
	return "ramp: payout finalize refused: " + e.Code
}

func (e *FinalizeRefusedError) Unwrap() error { return ErrProviderFailed }

// ErrOutcomeUnknown is a request that reached the provider with no answer
// back: the client timed out, the deadline passed, or the connection broke
// after the request was on it. Whether the provider acted on it cannot be
// known from here, so a create carrying it is sent again — the same bytes,
// the same Idempotency-Key — rather than failed or failed over.
//
// It is ErrProviderFailed too, so every caller that only knows that one reads
// it unchanged. An answer that came back and cannot be used — a 2xx that does
// not decode, or names no usable id — is not this: that is ErrProviderFailed
// alone.
var ErrOutcomeUnknown = fmt.Errorf("%w: no answer received", ErrProviderFailed)

// ErrBankAccountEchoMismatch: a provider answered a bank account that is not the one sent. The BlindPay adapter wraps it on every echo failure and the bank service matches it.
var ErrBankAccountEchoMismatch = fmt.Errorf("%w: bank account echo does not match", ErrProviderFailed)

// ErrBankAccountNameAmbiguous: a provider holds more than one live bank account
// under a name that is deterministic per revision and rail, so none can be
// linked. It is an operator's problem (a duplicate to remove), never the user's.
var ErrBankAccountNameAmbiguous = fmt.Errorf("%w: several bank accounts share one name", ErrProviderFailed)

// NotSent wraps the failure of a step that runs before a create's money-moving
// request, which therefore never left.
func NotSent(err error) error { return fmt.Errorf("%w: %w", ErrRequestNotSent, err) }

// KycBlocked is a compliance refusal that names who refused and what they still
// want. It is the errors.As target the HTTP layer and the capabilities read
// model both match on.
//
// A bare sentinel is not enough: a user asking for a token only one provider
// serves would be told "no provider available" while the provider that would
// serve it sat one KYC step away, unnamed and unreachable.
//
// Err is ErrKycPending or ErrKycRejected, and Unwrap is what keeps errors.Is on
// those two working through it — the code table matches sentinels, not types.
type KycBlocked struct {
	// Provider is the one the user should be sent to, opaque to the client:
	// the app returns it on the /kyc/{provider} routes and branches on nothing.
	Provider ProviderCode
	// Status is the KycStatus of that provider's row, for the operator reading
	// a log and for the screen that has to word the refusal.
	Status string
	// PendingSteps is what that provider still wants. The router leaves it
	// empty — it has no business importing the KYC vocabulary — and the service
	// layer fills it from a port.
	PendingSteps []string
	Err          error
}

func (e *KycBlocked) Error() string {
	return fmt.Sprintf("ramp: provider %s: %v (status %s)", e.Provider, e.Err, e.Status)
}

func (e *KycBlocked) Unwrap() error { return e.Err }

// LimitViolation is a rail limit refusal carrying the bound that was tripped.
//
// A bare sentinel is not enough here either: it tells the user "this amount is
// below the minimum" and leaves them to find the minimum by trying again, which
// on a screen that quotes in a token and limits in BRL is not a search they can
// run.
//
// Err is ErrAmountBelowMinimum or ErrAmountAboveMaximum, and Unwrap is what
// keeps errors.Is on those two working through it — the code table matches
// sentinels, not types.
type LimitViolation struct {
	// LimitBRL is the bound the amount violated, as configured on the rail.
	// Which bound it is, is what Err says.
	LimitBRL string
	// LimitToken is LimitBRL converted to the token of the operation, on the
	// quote that was just calculated. Empty where there is nothing to convert
	// with: the on-ramp checks before a quote exists, and the off-ramp leaves
	// it empty rather than guess when the conversion cannot be trusted.
	LimitToken string
	// LimitUSD is the bound of a dollar payout, as units with two places: a
	// bank rail's min/max or a USD payout rail's min_amount_usd (the floor the
	// payout must reach after XIP's fee). Set instead of LimitBRL, never with it.
	LimitUSD string
	Err      error
}

func (e *LimitViolation) Error() string {
	if e.LimitUSD != "" {
		return fmt.Sprintf("ramp: %v (limit %s USD)", e.Err, e.LimitUSD)
	}
	return fmt.Sprintf("ramp: %v (limit %s BRL)", e.Err, e.LimitBRL)
}

func (e *LimitViolation) Unwrap() error { return e.Err }

// KycRefusal is a provider's final answer to an application that created no
// customer: it will not take this person, and says why, where every other
// failed submission is only "not this time".
//
// It exists for the create that is refused over who the applicant is — at Ozav,
// a tax id another account's customer holds, or an e-mail its books already
// name. Treated as an ordinary failure, the row went back to not_started owing
// the whole application, and the app asked for it again on every visit (users
// 14 and 21 in production, 28/09/2026). The worker instead records the row as
// rejected with these categories, which ends the loop.
//
// The adapter owns the translation, as it does for a rejected customer's
// status; Err is the provider's failure underneath, which Unwrap keeps
// reachable for the timeline and the error code the row records.
type KycRefusal struct {
	// Categories are models.KycRejection* values.
	Categories []string
	// Details are the provider's own codes, for the admin only.
	Details []string
	Err     error
}

func (e *KycRefusal) Error() string { return e.Err.Error() }

func (e *KycRefusal) Unwrap() error { return e.Err }
