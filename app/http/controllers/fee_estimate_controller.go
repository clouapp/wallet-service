package controllers

import (
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/feeestimate"
)

// FeeEstimateController prices a withdrawal without signing or broadcasting.
type FeeEstimateController struct {
	estimates *feeestimate.Service
}

// NewFeeEstimateController requires the estimator. Routes resolve it from the container.
func NewFeeEstimateController(estimates *feeestimate.Service) *FeeEstimateController {
	if estimates == nil {
		panic("fee estimate controller: service is required")
	}
	return &FeeEstimateController{estimates: estimates}
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

// GetWalletFeeEstimate godoc
// @Summary      Estimate a withdrawal's network fee
// @Description  Prices the withdrawal POST /withdrawals would send, with the same planner and chain adapters, without signing or broadcasting. The fee is always in the chain's native coin. Answers are cached for FEE_ESTIMATE_CACHE_TTL_SECONDS (default 15). Node failures answer 503.
// @Tags         Wallet Withdrawals
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path   string  true   "Wallet UUID"  format(uuid)
// @Param        asset     query  string  false  "Asset symbol; the chain's native coin when omitted"
// @Param        amount    query  string  false  "Decimal amount, as sent to POST /withdrawals"
// @Param        to        query  string  false  "Destination address; a probe recipient when omitted"
// @Success      200  {object}  feeestimate.Estimate
// @Failure      400  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Failure      422  {object}  ErrorResponse
// @Failure      429  {object}  ErrorResponse
// @Failure      503  {object}  ErrorResponse
// @Router       /api/v1/wallets/{walletId}/fee-estimate [get]
func (ctrl *FeeEstimateController) GetWalletFeeEstimate(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)
	callerAccountID, _ := requestctx.AccountID(ctx)
	if callerAccountID == uuid.Nil && wallet.AccountID != nil {
		callerAccountID = *wallet.AccountID
	}

	var query requests.FeeEstimateRequest
	query.Load(ctx)
	estimate, err := ctrl.estimates.Estimate(ctx.Context(), feeestimate.Request{
		Wallet:          wallet,
		Asset:           query.Asset,
		Amount:          query.Amount,
		To:              query.To,
		CallerAccountID: callerAccountID,
	})
	if err != nil {
		return feeEstimateErrorResponse(ctx, wallet, err)
	}
	return responses.Send(ctx, http.StatusOK, estimate)
}

func feeEstimateErrorResponse(ctx http.Context, wallet *models.Wallet, err error) http.Response {
	var estimateErr *feeestimate.Error
	if !errors.As(err, &estimateErr) {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to estimate the fee"})
	}
	status := feeEstimateStatus(estimateErr.Kind)
	if status >= http.StatusInternalServerError {
		slog.Warn("fee estimate failed", "wallet_id", wallet.ID, "chain", wallet.Chain, "code", estimateErr.Code, "error", err)
	}
	return responses.Send(ctx, status, http.Json{"error": estimateErr.Message, "code": estimateErr.Code})
}
