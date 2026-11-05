package ramps

import (
	"errors"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	customerssvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/customers"
	kycsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	clientramps "github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/services/mfa"
)

// DefaultErrorCode is what an error nobody recognises maps to. It is the
// ramp package's constant, re-exported so the controllers keep one name.
const DefaultErrorCode = ramp.DefaultErrorCode

// ErrorCode maps a service error to the stable code the client contract
// exposes for it — never the upstream message, which can restate customer
// data.
//
// It lives here, not in the controllers package, so both sides of the client
// contract can read it without an import cycle: the controllers package
// already imports this one for KycResource and friends, and mapError there
// calls this function instead of carrying its own copy of the sentinel table.
// NewSubmissionCollection above calls it directly, being in the same package.
//
// The ramp sentinels resolve through ramp.ErrorCode, which the KYC service
// also reads when it records why a queued submission failed; only the
// service-level sentinels — the ones the ramp package cannot see — are named
// here.
func ErrorCode(err error) string {
	if code := ramp.ErrorCode(err); code != "" {
		return code
	}
	switch {
	// A wrong or expired step-up code is the user's own typo, and the only
	// MFA outcome another attempt fixes. It used to fall through to the
	// default, which answered 502 provider_unavailable: the app told the user
	// the service was down and stopped offering the field they needed to
	// correct. It is distinct from mfa_required, which means no code was sent
	// at all.
	case errors.Is(err, mfa.ErrInvalidCode):
		return "mfa_invalid"
	case errors.Is(err, kycsvc.ErrSubmissionInFlight):
		return "submission_in_flight"
	// Documents for a provider that creates its customer from the
	// application and the documents together, with no application held.
	case errors.Is(err, kycsvc.ErrApplicationRequired):
		return "application_required"
	// The person's one atomic create is held for an operator: nothing is in
	// flight, and nothing the app sends goes through until it is decided.
	case errors.Is(err, kycsvc.ErrSubmissionHeld):
		return "submission_held"
	// Money can still move at the provider on the customer a retry would
	// leave: the codes an unlink answers for the same state.
	case errors.Is(err, kycsvc.ErrVirtualAccountActive):
		return "virtual_account_active"
	case errors.Is(err, kycsvc.ErrTransactionOpen):
		return "transaction_open"
	case errors.Is(err, clientramps.ErrProvisioningInFlight):
		return "provisioning_in_flight"
	case errors.Is(err, clientramps.ErrInvalidOrderState):
		return "invalid_order_state"
	// The account was deleted under the request. The code is the one Auth
	// answers a gone account with, so the app does what it does for any
	// request of a deleted account: it ends the session.
	case errors.Is(err, clientramps.ErrAccountDeleted):
		return "unauthenticated"
	// The customer header named a customer the user is not a member of, or
	// none at all. The app drops the customer id it held (spec §6).
	case errors.Is(err, customerssvc.ErrCustomerNotFound):
		return "customer_not_found"
	// The customer is closed: it reads, it never moves money (contract §1).
	case errors.Is(err, customerssvc.ErrCustomerClosed):
		return "customer_closed"
	// Another holder holds the address the customer would register.
	case errors.Is(err, clientramps.ErrWalletInUse):
		return "wallet_in_use"
	// A person registers no wallet: the forbidden shape (round-2 review).
	case errors.Is(err, clientramps.ErrWalletNotForPerson):
		return "forbidden"
	// A business moves money only to and from an address it registered, and
	// pays out only to its own CNPJ key (spec §9, API contract §10).
	case errors.Is(err, clientramps.ErrWalletNotRegistered):
		return "wallet_not_registered"
	case errors.Is(err, clientramps.ErrHolderMismatch):
		return "holder_mismatch"
	case errors.Is(err, kycsvc.ErrEmailMismatch):
		return "email_mismatch"
	case errors.Is(err, kycsvc.ErrTaxIDInUse):
		return "tax_id_in_use"
	case errors.Is(err, kycsvc.ErrPhoneInUse):
		return "phone_in_use"
	case errors.Is(err, kycsvc.ErrBusinessExists):
		return "business_exists"
	case errors.Is(err, kycsvc.ErrDocumentsRequired):
		return "documents_required"
	case errors.Is(err, kycsvc.ErrDocumentTypeRequired):
		return "document_type_required"
	// Nobody enabled serves the residence and tax identity declared, or the
	// provider the path names does not. One code, no field: the app reads
	// from the countries catalog what each residence and identity opens.
	case errors.Is(err, kycsvc.ErrJurisdictionNotServed):
		return "jurisdiction_not_served"
	// Its own code beside not_found: the id a bank payout names is a field of
	// the body, and the app sends the user back to pick the account again.
	case errors.Is(err, clientramps.ErrBankBeneficiaryNotFound):
		return "bank_beneficiary_not_found"
	case errors.Is(err, clientramps.ErrEstimateNotFound), errors.Is(err, clientramps.ErrOrderNotFound),
		errors.Is(err, clientramps.ErrVirtualAccountNotFound), errors.Is(err, clientramps.ErrBankAccountNotFound):
		return "not_found"
	case errors.Is(err, clientramps.ErrDuplicateBankAccount):
		return "bank_account_already_exists"
	// The provider said the bank account does not take the rail asked (a wire
	// to an account whose beneficiary address it did not accept). The user
	// picks the other rail or another account; nothing was created.
	case errors.Is(err, clientramps.ErrPayoutRailUnsupportedForAccount):
		return "bank_account_rail_unsupported"
	// A USD payout address acting for a company: the bank account is a
	// member's own, and business USD payouts are not designed yet. The app
	// offers USD payouts on the person's own customer only.
	case errors.Is(err, clientramps.ErrBusinessPayoutUnsupported):
		return "business_payout_unsupported"
	// ErrProviderCredentialsInvalid, ErrIdempotencyConflict and
	// ErrUploadHostNotAllowed deliberately resolve to the default. Each is a
	// fault on XIP's side — a key to rotate, a collision we generated, a host
	// we refused to trust — that the user cannot act on and that would leak
	// internal state if named in a response.
	//
	// They share the code and no longer the status. The first two only ever
	// arrive inside a bridge or ozav *UpstreamError, which is
	// ramp.ErrProviderFailed whatever it wraps, so they keep the generic 502.
	// The third is produced here, against an answer we refused to act on, and
	// answers 500 — mapError logs it on the way out either way.
	default:
		return DefaultErrorCode
	}
}
