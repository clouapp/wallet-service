package controllers

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/feeestimate"
)

// mapFeeEstimateError answers a failed estimate with its own code and message:
// the error's kind picks the status, and a node failure (5xx) is logged with the
// provider cause, which stays out of the body. An error that is not an estimate
// error is the generic 500.
func mapFeeEstimateError(ctx http.Context, wallet *models.Wallet, err error) http.Response {
	var estimateErr *feeestimate.Error
	if !errors.As(err, &estimateErr) {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to estimate the fee")
	}
	status := feeEstimateStatus(estimateErr.Kind)
	if status >= http.StatusInternalServerError {
		slog.Warn("fee estimate failed", "wallet_id", wallet.ID, "chain", wallet.Chain, "code", estimateErr.Code, "error_type", fmt.Sprintf("%T", err), "provider_cause", chain.CauseText(err))
	}
	// newError, the only constructor of feeestimate.Error, always sets Code.
	return responses.Fail(ctx, status, estimateErr.Code, estimateErr.Message)
}

func feeEstimateStatus(kind feeestimate.Kind) int {
	switch kind {
	case feeestimate.KindInvalidInput:
		return http.StatusBadRequest
	case feeestimate.KindUnprocessable:
		return http.StatusUnprocessableEntity
	case feeestimate.KindRateLimited:
		return http.StatusTooManyRequests
	case feeestimate.KindUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
