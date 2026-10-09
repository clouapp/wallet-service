package feeestimate

import (
	"errors"
	"fmt"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/withdraw"
)

// Kind groups failures by what the caller can do about them.
type Kind string

const (
	// KindInvalidInput: the request itself is malformed (HTTP 400).
	KindInvalidInput Kind = "invalid_input"
	// KindUnprocessable: well formed, but this wallet cannot quote it (HTTP 422).
	KindUnprocessable Kind = "unprocessable"
	// KindRateLimited: a planner limit refused the request (HTTP 429).
	KindRateLimited Kind = "rate_limited"
	// KindUnavailable: a node could not answer; retry later (HTTP 503).
	KindUnavailable Kind = "unavailable"
)

// Machine-readable error codes returned to API clients.
const (
	CodeInvalidAmount        = "invalid_amount"
	CodeAmountBelowMinimum   = "amount_below_minimum"
	CodeUnknownAsset         = "unknown_asset"
	CodeInvalidAddress       = "invalid_address"
	CodeUnsupportedChain     = "unsupported_chain"
	CodeTokenBalanceRequired = "token_balance_required"
	CodeTooManyAddresses     = "sweep_limit_exceeded"
	CodeGasEstimateFailed    = "gas_estimate_failed"
	CodeUnavailable          = "fee_estimate_unavailable"
)

// Error is a classified estimate failure. Message is safe to show API clients.
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return e.Message
	}
	return fmt.Sprintf("%s: %v", e.Message, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

func newError(kind Kind, code, message string, cause error) *Error {
	return &Error{Kind: kind, Code: code, Message: message, Err: cause}
}

// classifyQuoteError maps a planner/adapter failure to an estimate error. Inputs are
// validated before quoting, so anything unrecognised is a node failure: the fee is
// unknown and no value is invented.
func classifyQuoteError(err error) *Error {
	switch {
	case errors.Is(err, sweep.ErrUnsupportedChain):
		return newError(KindUnprocessable, CodeUnsupportedChain, "fee estimates are not supported on this chain", err)
	case errors.Is(err, sweep.ErrFeeQuoteNeedsTokenBalance):
		return newError(KindUnprocessable, CodeTokenBalanceRequired, "the wallet holds none of this token, so its transfer cannot be simulated", err)
	case errors.Is(err, sweep.ErrTooManyAddresses):
		return newError(KindRateLimited, CodeTooManyAddresses, "the wallet has more addresses than one request may plan over", err)
	case errors.Is(err, sweep.ErrGasEstimateFailed), errors.Is(err, chain.ErrFee), errors.Is(err, chain.ErrNonce):
		return newError(KindUnavailable, CodeGasEstimateFailed, "the node could not size this transfer", err)
	case errors.Is(err, chain.ErrInvalidAddress):
		return newError(KindUnprocessable, CodeInvalidAddress, chain.ErrInvalidAddress.Error(), err)
	case errors.Is(err, chain.ErrInsufficientFunds):
		return newError(KindUnprocessable, CodeGasEstimateFailed, chain.ErrInsufficientFunds.Error(), err)
	case errors.Is(err, chain.ErrProviderUnavailable), errors.Is(err, chain.ErrProvider),
		errors.Is(err, chain.ErrNotFound), errors.Is(err, chain.ErrRateLimited):
		return newError(KindUnavailable, CodeUnavailable, "the chain node could not price this withdrawal; try again later", err)
	default:
		return newError(KindUnavailable, CodeUnavailable, "the chain node could not price this withdrawal; try again later", err)
	}
}

func classifyAssetError(err error) *Error {
	if errors.Is(err, withdraw.ErrUnknownAsset) {
		return newError(KindUnprocessable, CodeUnknownAsset, withdraw.ErrUnknownAsset.Error(), err)
	}
	return newError(KindInvalidInput, CodeInvalidAmount, err.Error(), err)
}
