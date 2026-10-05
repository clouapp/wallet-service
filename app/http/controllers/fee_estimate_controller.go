package controllers

import (
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/feeestimate"
	"github.com/macrowallets/waas/app/services/sweep"
)

const feeEstimateCacheTTLConfigKey = "fee_estimate.cache_ttl_seconds"

// feeEstimateStatus maps an estimate failure kind to its HTTP status.
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
// @Description  Prices the withdrawal POST /withdrawals would send, with the same planner and chain adapters, without signing or broadcasting. The fee is always in the chain's native coin (ETH, POL, BTC, LTC, SOL, TRX), also for tokens. EVM transfers are legacy transactions paying gas_price_wei for every unit of gas (plus the L1 data fee on OP-stack networks); Bitcoin and Litecoin use the node's fee rate and the coin selection the builder runs; Solana pays 5000 lamports per signature plus the recipient's token account when it must be created; TRON is priced as burned TRX with no free or staked resources (bandwidth per signed byte, 1.1 TRX to activate a new recipient, TRC-20 energy from estimateenergy at the chain's energy price), broken down in details.tron. When the wallet cannot cover the amount, the fee of the transfer from the base address is returned with insufficient_funds=true. Answers are cached for FEE_ESTIMATE_CACHE_TTL_SECONDS (default 15). Node failures answer 503; no value is guessed.
// @Tags         Wallet Withdrawals
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path   string  true   "Wallet UUID"  format(uuid)
// @Param        asset     query  string  false  "Asset symbol; the chain's native coin when omitted"  example(USDC)
// @Param        amount    query  string  false  "Decimal amount, as sent to POST /withdrawals; the smallest transfer when omitted"  example(0.001)
// @Param        to        query  string  false  "Destination address; a probe recipient when omitted"
// @Success      200  {object}  feeestimate.Estimate
// @Failure      400  {object}  FeeEstimateErrorResponse  "invalid_amount"
// @Failure      404  {object}  ErrorResponse  "wallet not found (or owned by another account)"
// @Failure      422  {object}  FeeEstimateErrorResponse  "unknown_asset, invalid_address, amount_below_minimum, token_balance_required, unsupported_chain"
// @Failure      429  {object}  FeeEstimateErrorResponse  "sweep_limit_exceeded"
// @Failure      503  {object}  FeeEstimateErrorResponse  "fee_estimate_unavailable, gas_estimate_failed"
// @Router       /api/v1/wallets/{walletId}/fee-estimate [get]
func GetWalletFeeEstimate(ctx http.Context) http.Response {
	wallet, ok := ctx.Value("wallet").(*models.Wallet)
	if !ok || wallet == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "wallet not found"})
	}

	service, err := newFeeEstimateService()
	if err != nil {
		return MapInternalError(ctx, err, "fee_estimate_wiring")
	}

	callerAccountID, _ := ctx.Value("account_id").(uuid.UUID)
	if callerAccountID == uuid.Nil && wallet.AccountID != nil {
		callerAccountID = *wallet.AccountID
	}

	estimate, err := service.Estimate(ctx.Context(), feeestimate.Request{
		Wallet:          wallet,
		Asset:           ctx.Request().Query("asset", ""),
		Amount:          ctx.Request().Query("amount", ""),
		To:              ctx.Request().Query("to", ""),
		CallerAccountID: callerAccountID,
	})
	if err != nil {
		return feeEstimateErrorResponse(ctx, wallet, err)
	}
	return ctx.Response().Json(http.StatusOK, estimate)
}

func newFeeEstimateService() (*feeestimate.Service, error) {
	c := container.Get()
	quoter, ok := c.SweepService.(sweep.FeeQuoter)
	if !ok {
		return nil, errors.New("sweep service cannot quote withdrawal fees")
	}
	ttl := time.Duration(facades.Config().GetInt(feeEstimateCacheTTLConfigKey, int(feeestimate.DefaultCacheTTL/time.Second))) * time.Second
	return feeestimate.NewService(quoter, c.Registry, c.ChainRepo, feeestimate.NewRedisCache(c.Redis), ttl, time.Now)
}

func feeEstimateErrorResponse(ctx http.Context, wallet *models.Wallet, err error) http.Response {
	var estimateErr *feeestimate.Error
	if !errors.As(err, &estimateErr) {
		return MapInternalError(ctx, err, "fee_estimate")
	}
	status := feeEstimateStatus(estimateErr.Kind)
	if status >= http.StatusInternalServerError {
		slog.Warn("fee estimate failed", "wallet_id", wallet.ID, "chain", wallet.Chain, "code", estimateErr.Code, "error", err)
	}
	return ctx.Response().Json(status, http.Json{"error": estimateErr.Message, "code": estimateErr.Code})
}

type FeeEstimateErrorResponse struct {
	Error string `json:"error" example:"the chain node could not price this withdrawal; try again later"`
	Code  string `json:"code" example:"fee_estimate_unavailable"`
}
