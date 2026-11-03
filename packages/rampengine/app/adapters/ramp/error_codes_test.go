package ramp

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrorCodeNamesTheTermsSessionFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"expired session", ErrTosSessionExpired, "tos_session_expired"},
		{"missing legal name", ErrTosLegalNameRequired, "tos_legal_name_required"},
		// Every adapter error reaches the HTTP layer wrapped with the
		// provider's name, so a table that only matches bare sentinels would
		// resolve each of these to the default in production.
		{"wrapped", fmt.Errorf("kyc: accept tos at ozav: %w", ErrTosSessionExpired), "tos_session_expired"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ErrorCode(tc.err))
		})
	}
}

// A rail the person's declaration does not open has its own code: the app
// shows its own refusal and offers the rail that is open, instead of a wizard
// (kyc_pending) or an outage (no_provider_available).
func TestErrorCodeNamesARailThePersonIsNotEligibleFor(t *testing.T) {
	assert.Equal(t, "rail_not_eligible", ErrorCode(ErrRailNotEligible))
	assert.Equal(t, "rail_not_eligible", ErrorCode(fmt.Errorf("onramp estimate: %w", ErrRailNotEligible)))
}

// An adapter's pre-call check of the KYC data is the user's to fix, not an
// outage: it has its own code, and it survives the adapter's wrapping.
func TestErrorCodeNamesInvalidKycData(t *testing.T) {
	assert.Equal(t, "kyc_data_invalid", ErrorCode(fmt.Errorf("blindpay: first_name is required: %w", ErrKycDataInvalid)))
}

// A rail the provider has not enabled for XIP is an outage of that rail to the
// user, not a credential to rotate.
func TestErrorCodeReadsADisabledProviderFeatureAsNoProvider(t *testing.T) {
	assert.Equal(t, "no_provider_available", ErrorCode(ErrProviderFeatureDisabled))
	assert.Equal(t, "no_provider_available", ErrorCode(fmt.Errorf("bridge: %w", ErrProviderFeatureDisabled)))
}

// A company e-mail the provider holds for another customer is the user's to
// change, and the app reopens the company step on it (API contract §5.5).
func TestErrorCodeNamesACompanyEmailInUse(t *testing.T) {
	assert.Equal(t, "email_in_use", ErrorCode(fmt.Errorf("kyc: submit to bridge: %w", ErrEmailInUse)))
}

// A document the provider refused is the category the app already words and
// whose steps it reopens, not an outage (provider_unavailable).
func TestErrorCodeNamesARefusedDocument(t *testing.T) {
	assert.Equal(t, "document_unreadable", ErrorCode(fmt.Errorf("bridge: PUT /customers/cus_1: %w", ErrDocumentRefused)))
}

// A wallet address the user typed that is not one is theirs to retype, not an
// outage (provider_unavailable) nor our fault (ErrInvalidDestination's 500).
func TestErrorCodeNamesAnInvalidWallet(t *testing.T) {
	assert.Equal(t, "invalid_wallet", ErrorCode(fmt.Errorf("blindpay: wallet address: %w", ErrInvalidWallet)))
}

// A bank account still under review asks the app to wait, and one the provider
// refused asks the user to fix the beneficiary: each has its own code, and
// both survive the bank service's wrapping.
func TestErrorCodeNamesTheBankAccountStates(t *testing.T) {
	assert.Equal(t, "bank_account_verifying", ErrorCode(ErrBankAccountVerifying))
	assert.Equal(t, "bank_account_verifying", ErrorCode(fmt.Errorf("bank: ensure account ba_1: %w", ErrBankAccountVerifying)))
	assert.Equal(t, "bank_account_rejected", ErrorCode(ErrBankAccountRejected))
	assert.Equal(t, "bank_account_rejected", ErrorCode(fmt.Errorf("bank: ensure account ba_1: %w", ErrBankAccountRejected)))
}

// The prepared funding's three refusals each have a code, surviving wrapping.
func TestErrorCodeNamesThePreparedFundingRefusals(t *testing.T) {
	assert.Equal(t, "invalid_signature", ErrorCode(fmt.Errorf("offramp: validate: %w", ErrInvalidSignedActionPayload)))
	assert.Equal(t, "funding_action_expired", ErrorCode(fmt.Errorf("offramp: finalize: %w", ErrFundingActionExpired)))
	assert.Equal(t, "beneficiary_changed", ErrorCode(fmt.Errorf("offramp: prepare: %w", ErrBeneficiaryChanged)))
}

// A finalize replayed with the same key and a different body is the client's
// to fix with a new key, and a finalize with no key is refused before anything
// is sent. Each has its own code, and both survive wrapping.
func TestErrorCodeNamesTheIdempotencyKeyFailures(t *testing.T) {
	assert.Equal(t, "idempotency_key_reused", ErrorCode(ErrIdempotencyKeyReused))
	assert.Equal(t, "idempotency_key_reused", ErrorCode(fmt.Errorf("offramp: finalize: %w", ErrIdempotencyKeyReused)))
	assert.Equal(t, "idempotency_key_required", ErrorCode(ErrInvalidIdempotencyKey))
	assert.Equal(t, "idempotency_key_required", ErrorCode(fmt.Errorf("offramp: finalize: %w", ErrInvalidIdempotencyKey)))
}

// The finalize sentinels that never reach a client on their own stay out of
// the table: the service turns a used quote into an ambiguous outcome, and a
// binding mismatch into a log line. They resolve to the default, as the
// internal faults do.
func TestErrorCodeLeavesTheFinalizeInternalsToTheService(t *testing.T) {
	assert.Equal(t, "", ErrorCode(ErrQuoteAlreadyUsed))
	assert.Equal(t, "", ErrorCode(ErrBindingMismatch))
	assert.Equal(t, "", ErrorCode(&FinalizeRefusedError{Code: "payout_rejected"}))
}
