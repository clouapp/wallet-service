package controllers

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/withdraw"
)

// MapInternalError logs the real error server-side and returns a generic 500
// body to the client. This is the single point through which controllers
// emit unmapped errors, so raw error strings (RPC URLs, DB messages, file
// paths, token registry details, …) never leak across the API boundary.
// `endpoint` is a short stable label used for log filtering / alerting
// (e.g. "consolidate", "preview_withdraw").
func MapInternalError(ctx http.Context, err error, endpoint string) http.Response {
	slog.Error("controller internal error",
		"endpoint", endpoint,
		"error", err,
	)
	return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternalError, "internal_error")
}

// MapSweepError maps sentinel errors from the sweep package to HTTP responses
// consumed by dashboard + external API controllers.
// Returns nil if the error is not a recognized sentinel (caller should fall back
// to a generic 500 or 400).
func MapSweepError(ctx http.Context, err error) http.Response {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, sweep.ErrInFlightConsolidation):
		return responses.FailWith(ctx, http.StatusTooManyRequests, responses.CodeSweepLimitExceeded, "sweep_limit_exceeded", map[string]any{"limit_type": "in_flight_consolidation", "retry_after_seconds": 60})
	case errors.Is(err, sweep.ErrDailyQuotaExceeded):
		return responses.FailWith(ctx, http.StatusTooManyRequests, responses.CodeSweepLimitExceeded, "sweep_limit_exceeded", map[string]any{"limit_type": "daily_quota"})
	case errors.Is(err, sweep.ErrTooManyAddresses):
		return responses.FailWith(ctx, http.StatusTooManyRequests, responses.CodeSweepLimitExceeded, "sweep_limit_exceeded", map[string]any{"limit_type": "addresses_per_request"})
	case errors.Is(err, sweep.ErrWalletNotGasReady):
		return responses.FailWith(ctx, http.StatusUnprocessableEntity, responses.CodeWalletNotGasReady, "wallet_not_gas_ready", map[string]any{"action": "fund_base_address"})
	case errors.Is(err, sweep.ErrInsufficientFunds):
		return responses.Fail(ctx, http.StatusUnprocessableEntity, responses.CodeInsufficientFunds, "insufficient_funds")
	case errors.Is(err, sweep.ErrUnsupportedChain):
		return responses.Fail(ctx, http.StatusUnprocessableEntity, responses.CodeUnsupportedChain, "unsupported_chain")
	case errors.Is(err, sweep.ErrGasEstimateFailed):
		return responses.Fail(ctx, http.StatusUnprocessableEntity, "gas_estimate_failed", "gas_estimate_failed")
	}
	return nil
}

// MapWithdrawalCreateError maps a refusal from withdraw.Service.Create.
// The body stays the legacy {"error": message} map, which the writer wraps
// into {"error":{"code","message"}}. A row failure is a generic 500.
func MapWithdrawalCreateError(ctx http.Context, err error) http.Response {
	var refusal *withdraw.CreateRefusal
	if errors.As(err, &refusal) && refusal != nil {
		return mapWithdrawalRefusal(ctx, refusal)
	}
	var row *withdraw.CreateRowError
	if errors.As(err, &row) && row != nil {
		cause := row.Err
		if cause == nil {
			cause = err
		}
		endpoint := row.Endpoint
		if endpoint == "" {
			endpoint = "create_wallet_withdrawal"
		}
		return MapInternalError(ctx, cause, endpoint)
	}
	return MapInternalError(ctx, err, "create_wallet_withdrawal")
}

// mapWithdrawalRefusal keeps a caller-fixable refusal on the status the
// service chose. A chain the registry does not know is an outage, and an unknown
// asset names what the customer typed, so neither of those texts is returned.
func mapWithdrawalRefusal(ctx http.Context, refusal *withdraw.CreateRefusal) http.Response {
	if errors.Is(refusal, chainregistry.ErrUnknownChain) {
		slog.Error("create withdrawal chain unavailable")
		return responses.Error(ctx, http.StatusInternalServerError, responses.CodeInternal, "internal error")
	}
	message := refusal.Message
	if strings.HasPrefix(message, "unknown asset ") {
		message = "unknown asset"
	}
	return responses.FailMessage(ctx, createRefusalStatus(refusal.Status), message)
}

func createRefusalStatus(status withdraw.CreateStatus) int {
	switch status {
	case withdraw.CreateStatusUnauthorized:
		return http.StatusUnauthorized
	case withdraw.CreateStatusForbidden:
		return http.StatusForbidden
	case withdraw.CreateStatusBadRequest:
		return http.StatusBadRequest
	case withdraw.CreateStatusUnprocessable:
		return http.StatusUnprocessableEntity
	case withdraw.CreateStatusTooManyRequests:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}

// MapSpendingLimitError maps a per-token daily USD cap failure. A blank cap
// never produces these sentinels. The body uses the same legacy error map as
// the sweep quota, so the envelope keeps limit_type beside the code.
func MapSpendingLimitError(ctx http.Context, err error) http.Response {
	switch {
	case errors.Is(err, withdraw.ErrSpendingLimitExceeded):
		return responses.FailWith(ctx, http.StatusTooManyRequests, responses.CodeSpendingLimitExceeded, "spending_limit_exceeded", map[string]any{"limit_type": "daily_usd"})
	case errors.Is(err, withdraw.ErrSpendingLimitInvalid):
		return responses.Fail(ctx, http.StatusUnprocessableEntity, responses.CodeSpendingLimitInvalid, "spending_limit_invalid")
	case errors.Is(err, withdraw.ErrSpendingQuoteUnavailable):
		return responses.Fail(ctx, http.StatusServiceUnavailable, responses.CodeSpendingLimitQuoteUnavailable, "spending_limit_quote_unavailable")
	default:
		return nil
	}
}
