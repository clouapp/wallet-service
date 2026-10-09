package withdraw

import (
	"errors"

	"github.com/macrowallets/waas/app/services/sweep"
)

// Public failure codes persisted on withdrawals.failure_reason. They are the
// closed code list clients read. Raw error strings (RPC URLs, DB messages)
// never are.
const (
	FailureInsufficientFunds    = "insufficient_funds"
	FailureWalletNotGasReady    = "wallet_not_gas_ready"
	FailureUnsupportedChain     = "unsupported_chain"
	FailureSweepLimitExceeded   = "sweep_limit_exceeded"
	FailureInvalidPassphrase    = "invalid_passphrase"
	FailurePassphraseTooShort   = "passphrase_too_short"
	FailureConcurrentWithdrawal = "concurrent_withdrawal"
	FailureTooManyAttempts      = "too_many_attempts"
	FailureSpendingLimit        = "spending_limit_exceeded"
	FailureSpendingLimitInvalid = "spending_limit_invalid"
	FailureSpendingQuote        = "spending_limit_quote_unavailable"
	FailureInternalError        = "internal_error"
)

// FailureCode is the failure_reason a failed Request leaves on the row.
func FailureCode(err error) string {
	switch {
	case err == nil:
		return FailureInternalError
	case errors.Is(err, sweep.ErrInsufficientFunds), errors.Is(err, ErrInsufficientFunds):
		return FailureInsufficientFunds
	case errors.Is(err, sweep.ErrWalletNotGasReady):
		return FailureWalletNotGasReady
	case errors.Is(err, sweep.ErrUnsupportedChain):
		return FailureUnsupportedChain
	case errors.Is(err, sweep.ErrInFlightConsolidation),
		errors.Is(err, sweep.ErrDailyQuotaExceeded),
		errors.Is(err, sweep.ErrTooManyAddresses):
		return FailureSweepLimitExceeded
	case errors.Is(err, ErrInvalidPassphrase):
		return FailureInvalidPassphrase
	case errors.Is(err, ErrPassphraseTooShort):
		return FailurePassphraseTooShort
	case errors.Is(err, ErrConcurrentWithdraw):
		return FailureConcurrentWithdrawal
	case errors.Is(err, ErrTooManyAttempts):
		return FailureTooManyAttempts
	case errors.Is(err, ErrSpendingLimitExceeded):
		return FailureSpendingLimit
	case errors.Is(err, ErrSpendingLimitInvalid):
		return FailureSpendingLimitInvalid
	case errors.Is(err, ErrSpendingQuoteUnavailable):
		return FailureSpendingQuote
	default:
		return FailureInternalError
	}
}
