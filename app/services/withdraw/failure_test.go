package withdraw

import (
	"errors"
	"fmt"
	"testing"

	"github.com/macrowallets/waas/app/services/sweep"
)

func TestFailure_Code_NeverLeaksRawErrors(t *testing.T) {
	published := map[string]string{
		FailureInsufficientFunds:    "insufficient_funds",
		FailureWalletNotGasReady:    "wallet_not_gas_ready",
		FailureUnsupportedChain:     "unsupported_chain",
		FailureSweepLimitExceeded:   "sweep_limit_exceeded",
		FailureInvalidPassphrase:    "invalid_passphrase",
		FailurePassphraseTooShort:   "passphrase_too_short",
		FailureConcurrentWithdrawal: "concurrent_withdrawal",
		FailureTooManyAttempts:      "too_many_attempts",
		FailureSpendingLimit:        "spending_limit_exceeded",
		FailureSpendingLimitInvalid: "spending_limit_invalid",
		FailureSpendingQuote:        "spending_limit_quote_unavailable",
		FailureInternalError:        "internal_error",
	}
	for got, want := range published {
		if got != want {
			t.Fatalf("published code %q, want %q", got, want)
		}
	}

	cases := map[string]struct {
		err  error
		want string
	}{
		"sweep insufficient funds":    {sweep.ErrInsufficientFunds, FailureInsufficientFunds},
		"withdraw insufficient funds": {ErrInsufficientFunds, FailureInsufficientFunds},
		"wrapped not gas ready":       {fmt.Errorf("plan: %w", sweep.ErrWalletNotGasReady), FailureWalletNotGasReady},
		"unsupported chain":           {sweep.ErrUnsupportedChain, FailureUnsupportedChain},
		"daily quota":                 {sweep.ErrDailyQuotaExceeded, FailureSweepLimitExceeded},
		"invalid passphrase":          {ErrInvalidPassphrase, FailureInvalidPassphrase},
		"short passphrase":            {ErrPassphraseTooShort, FailurePassphraseTooShort},
		"concurrent withdrawal":       {ErrConcurrentWithdraw, FailureConcurrentWithdrawal},
		"too many attempts":           {ErrTooManyAttempts, FailureTooManyAttempts},
		"spending limit exceeded":     {ErrSpendingLimitExceeded, FailureSpendingLimit},
		"spending limit invalid":      {ErrSpendingLimitInvalid, FailureSpendingLimitInvalid},
		"spending quote unavailable":  {ErrSpendingQuoteUnavailable, FailureSpendingQuote},
		"rpc error with url":          {errors.New("dial tcp https://rpc.example/secret-key: timeout"), FailureInternalError},
		"nil error":                   {nil, FailureInternalError},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := FailureCode(tc.err); got != tc.want {
				t.Fatalf("FailureCode(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}
