package controllers

import (
	"errors"

	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/withdraw"
)

// Public failure codes persisted on withdrawals.failure_reason. They are safe to
// expose to API clients: raw error strings (RPC URLs, DB messages) never are.
const (
	WithdrawalFailureInsufficientFunds    = "insufficient_funds"
	WithdrawalFailureWalletNotGasReady    = "wallet_not_gas_ready"
	WithdrawalFailureUnsupportedChain     = "unsupported_chain"
	WithdrawalFailureSweepLimitExceeded   = "sweep_limit_exceeded"
	WithdrawalFailureInvalidPassphrase    = "invalid_passphrase"
	WithdrawalFailurePassphraseTooShort   = "passphrase_too_short"
	WithdrawalFailureConcurrentWithdrawal = "concurrent_withdrawal"
	WithdrawalFailureTooManyAttempts      = "too_many_attempts"
	WithdrawalFailureSpendingLimit        = "spending_limit_exceeded"
	WithdrawalFailureSpendingLimitInvalid = "spending_limit_invalid"
	WithdrawalFailureSpendingQuote        = "spending_limit_quote_unavailable"
	WithdrawalFailureInternalError        = "internal_error"
)

// WithdrawalLookupResponse is the external view of a withdrawal's outcome.
// TxHash is set only once a transaction exists on chain; FailureReason only for failed withdrawals.
type WithdrawalLookupResponse struct {
	ID                 string           `json:"id" example:"80571fff-8d0b-5c1e-9a3f-2b6f0f7c1a11"`
	IdempotencyKey     string           `json:"idempotency_key" example:"80571fff-8d0b-5c1e-9a3f-2b6f0f7c1a11"`
	WalletID           string           `json:"wallet_id"`
	Status             string           `json:"status" example:"broadcast"`
	Amount             string           `json:"amount" example:"4"`
	DestinationAddress string           `json:"destination_address"`
	TxHash             *string          `json:"tx_hash"`
	TransactionStatus  *string          `json:"transaction_status" example:"confirming"`
	FailureReason      *string          `json:"failure_reason" example:"insufficient_funds"`
	CreatedAt          *carbon.DateTime `json:"created_at" swaggertype:"string"`
	UpdatedAt          *carbon.DateTime `json:"updated_at" swaggertype:"string"`
}

// WithdrawalFailureCode is the persisted failure_reason both withdrawal surfaces write.
func WithdrawalFailureCode(err error) string {
	return withdrawalFailureCode(err)
}

func withdrawalFailureCode(err error) string {
	switch {
	case err == nil:
		return WithdrawalFailureInternalError
	case errors.Is(err, sweep.ErrInsufficientFunds), errors.Is(err, withdraw.ErrInsufficientFunds):
		return WithdrawalFailureInsufficientFunds
	case errors.Is(err, sweep.ErrWalletNotGasReady):
		return WithdrawalFailureWalletNotGasReady
	case errors.Is(err, sweep.ErrUnsupportedChain):
		return WithdrawalFailureUnsupportedChain
	case errors.Is(err, sweep.ErrInFlightConsolidation),
		errors.Is(err, sweep.ErrDailyQuotaExceeded),
		errors.Is(err, sweep.ErrTooManyAddresses):
		return WithdrawalFailureSweepLimitExceeded
	case errors.Is(err, withdraw.ErrInvalidPassphrase):
		return WithdrawalFailureInvalidPassphrase
	case errors.Is(err, withdraw.ErrPassphraseTooShort):
		return WithdrawalFailurePassphraseTooShort
	case errors.Is(err, withdraw.ErrConcurrentWithdraw):
		return WithdrawalFailureConcurrentWithdrawal
	case errors.Is(err, withdraw.ErrTooManyAttempts):
		return WithdrawalFailureTooManyAttempts
	case errors.Is(err, withdraw.ErrSpendingLimitExceeded):
		return WithdrawalFailureSpendingLimit
	case errors.Is(err, withdraw.ErrSpendingLimitInvalid):
		return WithdrawalFailureSpendingLimitInvalid
	case errors.Is(err, withdraw.ErrSpendingQuoteUnavailable):
		return WithdrawalFailureSpendingQuote
	default:
		return WithdrawalFailureInternalError
	}
}
