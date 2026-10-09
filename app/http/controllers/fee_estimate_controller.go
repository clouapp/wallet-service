package controllers

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/services/feeestimate"
)

// FeeEstimateController prices a withdrawal without signing or broadcasting.
// Both HTTP surfaces mount it on their wallet routes.
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

// Show godoc
//
//	@Summary		Estimate a withdrawal's network fee
//	@Description	Prices the withdrawal POST /withdrawals would send, with the same planner and chain adapters, without signing or broadcasting. The fee is always in the chain's native coin (ETH, POL, BTC, LTC, SOL, TRX), also for tokens. EVM transfers are legacy transactions paying gas_price_wei for every unit of gas (plus the L1 data fee on OP-stack networks); Bitcoin and Litecoin use the node's fee rate and the coin selection the builder runs; Solana pays 5000 lamports per signature plus the recipient's token account when it must be created; TRON is priced as burned TRX with no free or staked resources (bandwidth per signed byte, 1.1 TRX to activate a new recipient, TRC-20 energy from estimateenergy at the chain's energy price), broken down in details.tron. When the wallet cannot cover the amount, the fee of the transfer from the base address is returned with insufficient_funds=true. Answers are cached for FEE_ESTIMATE_CACHE_TTL_SECONDS (default 15). Node failures answer 503; no value is guessed.
//	@Tags			Wallet Withdrawals
//	@Security		BearerAuth
//	@Produce		json
//	@Param			walletId	path		string	true	"Wallet UUID"	format(uuid)
//	@Param			asset		query		string	false	"Asset symbol; the chain's native coin when omitted"
//	@Param			amount		query		string	false	"Decimal amount, as sent to POST /withdrawals"
//	@Param			to			query		string	false	"Destination address; a probe recipient when omitted"
//	@Success		200			{object}	feeestimate.Estimate
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Failure		422			{object}	responses.ErrorBody
//	@Failure		429			{object}	responses.ErrorBody
//	@Failure		503			{object}	responses.ErrorBody
//	@Router			/api/v1/wallets/{walletId}/fee-estimate [get]
func (c *FeeEstimateController) Show(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)
	callerAccountID, _ := requestctx.AccountID(ctx)

	estimate, err := c.estimates.Estimate(ctx.Context(), feeestimate.Request{
		Wallet:          wallet,
		Asset:           ctx.Request().Query("asset"),
		Amount:          ctx.Request().Query("amount"),
		To:              ctx.Request().Query("to"),
		CallerAccountID: callerAccountID,
	})
	if err != nil {
		return mapFeeEstimateError(ctx, wallet, err)
	}

	return ctx.Response().Success().Json(estimate)
}
