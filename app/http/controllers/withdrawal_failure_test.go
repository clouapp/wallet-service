package controllers

import (
	"errors"
	"fmt"
	"testing"

	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/withdraw"
)

func TestWithdrawalFailureCodeNeverLeaksRawErrors(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"sweep insufficient funds":    {sweep.ErrInsufficientFunds, WithdrawalFailureInsufficientFunds},
		"withdraw insufficient funds": {withdraw.ErrInsufficientFunds, WithdrawalFailureInsufficientFunds},
		"wrapped not gas ready":       {fmt.Errorf("plan: %w", sweep.ErrWalletNotGasReady), WithdrawalFailureWalletNotGasReady},
		"unsupported chain":           {sweep.ErrUnsupportedChain, WithdrawalFailureUnsupportedChain},
		"daily quota":                 {sweep.ErrDailyQuotaExceeded, WithdrawalFailureSweepLimitExceeded},
		"invalid passphrase":          {withdraw.ErrInvalidPassphrase, WithdrawalFailureInvalidPassphrase},
		"short passphrase":            {withdraw.ErrPassphraseTooShort, WithdrawalFailurePassphraseTooShort},
		"concurrent withdrawal":       {withdraw.ErrConcurrentWithdraw, WithdrawalFailureConcurrentWithdrawal},
		"too many attempts":           {withdraw.ErrTooManyAttempts, WithdrawalFailureTooManyAttempts},
		"spending limit exceeded":     {withdraw.ErrSpendingLimitExceeded, WithdrawalFailureSpendingLimit},
		"spending limit invalid":      {withdraw.ErrSpendingLimitInvalid, WithdrawalFailureSpendingLimitInvalid},
		"spending quote unavailable":  {withdraw.ErrSpendingQuoteUnavailable, WithdrawalFailureSpendingQuote},
		"rpc error with url":          {errors.New("dial tcp https://rpc.example/secret-key: timeout"), WithdrawalFailureInternalError},
		"nil error":                   {nil, WithdrawalFailureInternalError},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := withdrawalFailureCode(tc.err); got != tc.want {
				t.Fatalf("withdrawalFailureCode(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}
