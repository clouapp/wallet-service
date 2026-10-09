package controllers

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	sweeprequests "github.com/macrowallets/waas/app/http/requests/sweep"
	sweepresources "github.com/macrowallets/waas/app/http/resources/sweep"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/features"
	sweep "github.com/macrowallets/waas/app/services/sweep"
)

// SweepHandler serves the consolidate, gas and withdraw-preview routes both
// HTTP surfaces (dashboard and external) expose; each surface mounts it on its
// own routes and guards.
type SweepHandler struct {
	sweeps sweep.Service
	flags  *features.Service
}

// SweepHandlerDeps is everything the sweep handler needs.
// Sweeps and Flags are required.
type SweepHandlerDeps struct {
	Sweeps sweep.Service
	Flags  *features.Service
}

// NewSweepHandler wires the sweep handlers from SweepHandlerDeps. surface
// ("dashboard" or "external") only names the surface in the panic messages.
func NewSweepHandler(surface string, deps SweepHandlerDeps) *SweepHandler {
	if deps.Sweeps == nil {
		panic(surface + " sweep controller: sweep service is required")
	}
	if deps.Flags == nil {
		panic(surface + " sweep controller: feature flags are required")
	}
	return &SweepHandler{
		sweeps: deps.Sweeps,
		flags:  deps.Flags,
	}
}

// Consolidate godoc
//
//	@Summary		Consolidate wallet balances
//	@Description	Sweep all eligible child addresses' balance into the wallet's base deposit address.
//	@Tags			Wallets
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		BearerAuth
//	@Param			walletId	path		string						true	"Wallet UUID"	format(uuid)
//	@Param			body		body		ConsolidateRequestSwagger	true	"Consolidation request"
//	@Success		200			{object}	ConsolidateResponse
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		422			{object}	responses.ErrorBody
//	@Failure		429			{object}	responses.ErrorBody
//	@Router			/v1/wallets/{walletId}/consolidate [post]
func (c *SweepHandler) Consolidate(ctx http.Context) http.Response {
	wallet, _ := requestctx.Wallet(ctx)
	callerAccountID, _ := requestctx.AccountID(ctx)
	if response := BlockFlag(ctx, c.flags, AccountIDForWallet(ctx, wallet), features.FlagSweepEnabled, features.CodeSweepPaused, "consolidate"); response != nil {
		return response
	}

	walletID, err := requests.RouteUUID(ctx, "walletId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid wallet id")
	}

	var req sweeprequests.ConsolidateRequest
	defer DiscardPassphrase(&req.Passphrase)
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	result, err := c.sweeps.ConsolidateAll(ctx.Context(), walletID, req.Asset, req.Passphrase, callerAccountID)
	if err != nil {
		return mapSweepFailure(ctx, err, "consolidate")
	}

	return ctx.Response().Success().Json(sweepresources.NewConsolidation(result))
}

// GasStatus godoc
//
//	@Summary		Get wallet gas readiness
//	@Description	Returns the gas readiness status, native balance, and configured threshold for a wallet's base address.
//	@Tags			Wallets
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		BearerAuth
//	@Param			walletId	path		string	true	"Wallet UUID"	format(uuid)
//	@Success		200			{object}	GasStatusResponse
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		422			{object}	responses.ErrorBody
//	@Failure		429			{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/v1/wallets/{walletId}/gas-status [get]
func (c *SweepHandler) GasStatus(ctx http.Context) http.Response {
	walletID, err := requests.RouteUUID(ctx, "walletId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid wallet id")
	}

	status, err := c.sweeps.RefreshGasStatus(ctx.Context(), walletID)
	if err != nil {
		return mapSweepFailure(ctx, err, "gas_status")
	}

	return ctx.Response().Success().Json(sweepresources.NewGasStatus(status))
}

// GasCheck godoc
//
//	@Summary		Force refresh of wallet gas status
//	@Description	Action variant of gas-status. Forces an on-chain read. Rate-limited per wallet.
//	@Tags			Wallets
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		BearerAuth
//	@Param			walletId	path		string	true	"Wallet UUID"	format(uuid)
//	@Success		200			{object}	GasStatusResponse
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		422			{object}	responses.ErrorBody
//	@Failure		429			{object}	responses.ErrorBody
//	@Router			/v1/wallets/{walletId}/gas-check [post]
func (c *SweepHandler) GasCheck(ctx http.Context) http.Response {
	// The per-wallet rate limit is middleware.Throttle(ThrottleGasCheck) on the route.
	return c.GasStatus(ctx)
}

// Preview godoc
//
//	@Summary		Preview a withdrawal
//	@Description	Returns the planned sweep strategy without executing any transaction.
//	@Tags			Wallets
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		BearerAuth
//	@Param			walletId	path		string							true	"Wallet UUID"	format(uuid)
//	@Param			body		body		WithdrawPreviewRequestSwagger	true	"Preview request"
//	@Success		200			{object}	WithdrawPreviewResponse
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		422			{object}	responses.ErrorBody
//	@Failure		429			{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/v1/wallets/{walletId}/withdraw/preview [post]
func (c *SweepHandler) Preview(ctx http.Context) http.Response {
	callerAccountID, _ := requestctx.AccountID(ctx)

	walletID, err := requests.RouteUUID(ctx, "walletId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid wallet id")
	}

	var req sweeprequests.PreviewRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	plan, err := sweep.Preview(ctx.Context(), c.sweeps, sweep.PreviewInput{
		WalletID:        walletID,
		Asset:           req.Asset,
		Amount:          req.Amount,
		CallerAccountID: callerAccountID,
	})
	if err != nil {
		return mapSweepFailure(ctx, err, "preview_withdraw")
	}

	return ctx.Response().Success().Json(sweepresources.NewPreview(plan))
}
