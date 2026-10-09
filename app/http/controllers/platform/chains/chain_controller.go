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
}

// NewChainController wires the platform chain handlers.
func NewChainController(thresholds *chainsvc.Thresholds, rpc *chainsvc.RPC) *ChainController {
	if thresholds == nil {
		panic("platform chains controller: thresholds service is required")
	}
	if rpc == nil {
		panic("platform chains controller: rpc service is required")
	}
	return &ChainController{thresholds: thresholds, rpc: rpc}
}

// Update godoc
//
//	@Summary		Update chain sweep thresholds
//	@Description	Writes gas_readiness_threshold_raw, dust_threshold_native_raw, and dust_threshold_usd. An omitted field is left unchanged. An unknown chain is 404 for a platform admin. A negative amount is 422 and is not stored. The RPC URL is not accepted and is not returned.
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
