package chains

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	chainsrequests "github.com/macrowallets/waas/app/http/requests/platform/chains"
	"github.com/macrowallets/waas/app/http/responses"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

// ChainController edits the sweep thresholds and the RPC endpoint stored on
// a chain row. S1.4.7 names chains.view and chains.update on the catalog
// entry these routes already use. There is no platform permission catalog,
// so a platform_admins row is the gate, the same gate the other /v1/platform
// routes use. The pair is not a second gate. An unknown chain is 404 before
// that check. The RPC URL is write-only and is never returned.
type ChainController struct {
	thresholds *chainsvc.Thresholds
	rpc        *chainsvc.RPC
	endpoints  *chainsvc.RPCEndpoints
}

// ChainControllerDeps is the services the platform chain handlers call. All are required.
type ChainControllerDeps struct {
	Thresholds *chainsvc.Thresholds
	RPC        *chainsvc.RPC
	Endpoints  *chainsvc.RPCEndpoints
}

// NewChainController wires the platform chain handlers.
func NewChainController(deps ChainControllerDeps) *ChainController {
	if deps.Thresholds == nil {
		panic("platform chains controller: thresholds service is required")
	}
	if deps.RPC == nil {
		panic("platform chains controller: rpc service is required")
	}
	if deps.Endpoints == nil {
		panic("platform chains controller: rpc endpoints service is required")
	}
	return &ChainController{thresholds: deps.Thresholds, rpc: deps.RPC, endpoints: deps.Endpoints}
}

// Update godoc
//
//	@Summary		Update chain sweep thresholds, confirmations and finality
//	@Description	Writes gas_readiness_threshold_raw, dust_threshold_native_raw, dust_threshold_usd, required_confirmations and finality. An omitted field is left unchanged. An unknown chain is 404 for a platform admin. A negative amount or a confirmation count below 1 is 422 and is not stored. finality is confirmations, finalized_tag (EVM chains only) or solidified (TRON chains only); another value, or one the chain cannot read, is 422. Moving a chain from finalized_tag or solidified to confirmations needs required_confirmations in the same body (422 otherwise). Both apply to the transactions recorded after the chain registry next loads the row. The RPC URL is not accepted and is not returned.
//	@Tags			Platform Chains
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			chainId	path		string	true	"Chain id"
//	@Success		200		{object}	chainsvc.ThresholdView
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		403		{object}	responses.ErrorBody
//	@Failure		404		{object}	responses.ErrorBody
//	@Failure		422		{object}	responses.ErrorBody
//	@Router			/platform/chains/{chainId} [patch]
func (c *ChainController) Update(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	chainID, err := requests.RouteParam(ctx, "chainId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "chainId is required")
	}
	var req chainsrequests.UpdateRequest
	if err := req.Decode(ctx); err != nil {
		return mapError(ctx, err, "update chain thresholds")
	}

	view, err := c.thresholds.Update(ctx.Context(), actorID, chainID, req.Fields)
	if err != nil {
		return mapError(ctx, err, "update chain thresholds")
	}

	return ctx.Response().Success().Json(view)
}

// UpdateRPC godoc
//
//	@Summary		Replace a chain RPC endpoint
//	@Description	Seals a new rpc_url. The answer is rpcUrlSet. The URL is not returned. An unknown chain is 404 for a platform admin. An empty URL is 422 and is not stored.
//	@Tags			Platform Chains
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			chainId	path		string	true	"Chain id"
//	@Success		200		{object}	chainsvc.RPCView
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		403		{object}	responses.ErrorBody
//	@Failure		404		{object}	responses.ErrorBody
//	@Failure		422		{object}	responses.ErrorBody
//	@Router			/platform/chains/{chainId}/rpc [patch]
func (c *ChainController) UpdateRPC(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	chainID, err := requests.RouteParam(ctx, "chainId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "chainId is required")
	}
	var req chainsrequests.UpdateRPCRequest
	if err := req.Decode(ctx); err != nil {
		return mapError(ctx, err, "update chain rpc")
	}

	view, err := c.rpc.Update(ctx.Context(), actorID, chainID, req.Fields)
	if err != nil {
		return mapError(ctx, err, "update chain rpc")
	}

	return ctx.Response().Success().Json(view)
}
