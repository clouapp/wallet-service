package controllers

import (
	"errors"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/services/sweep"
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
	return ctx.Response().Json(http.StatusInternalServerError, http.Json{
		"error": "internal_error",
	})
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
		return ctx.Response().Json(http.StatusTooManyRequests, http.Json{
			"error":               "sweep_limit_exceeded",
			"limit_type":          "in_flight_consolidation",
			"retry_after_seconds": 60,
		})
	case errors.Is(err, sweep.ErrDailyQuotaExceeded):
		return ctx.Response().Json(http.StatusTooManyRequests, http.Json{
			"error":      "sweep_limit_exceeded",
			"limit_type": "daily_quota",
		})
	case errors.Is(err, sweep.ErrTooManyAddresses):
		return ctx.Response().Json(http.StatusTooManyRequests, http.Json{
			"error":      "sweep_limit_exceeded",
			"limit_type": "addresses_per_request",
		})
	case errors.Is(err, sweep.ErrWalletNotGasReady):
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{
			"error":  "wallet_not_gas_ready",
			"action": "fund_base_address",
		})
	case errors.Is(err, sweep.ErrInsufficientFunds):
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{
			"error": "insufficient_funds",
		})
	case errors.Is(err, sweep.ErrUnsupportedChain):
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{
			"error": "unsupported_chain",
		})
	}
	return nil
}
