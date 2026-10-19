package chains

import (
	"strconv"
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	chainsrequests "github.com/macrowallets/waas/app/http/requests/platform/chains"
	chainsresources "github.com/macrowallets/waas/app/http/resources/platform/chains"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/rpcendpoints"
)

// ListRPCEndpoints godoc
//
//	@Summary		List a chain's RPC endpoints
//	@Description	The ordered endpoints of the chain: position 1 is the primary (rpc_url), the others are the fallbacks tried in order when the ones before cannot serve a call. Each endpoint is its host and a fingerprint of its URL; no URL is returned. health is what a running process last recorded for it (consecutive failures, the cool-down it is skipped until, the last error, whether it serves another network); null when none did. With probe=true each endpoint is asked now for its network and head. failsOver is false for a chain whose adapter does not use the fallbacks (Bitcoin family, TRON, XRP). An unknown chain is 404 for a platform admin.
//	@Tags			Platform Chains
//	@Security		BearerAuth
//	@Produce		json
//	@Param			chainId	path		string	true	"Chain id"
//	@Param			probe	query		bool	false	"Ask every endpoint now"
//	@Success		200		{object}	chainsresources.RPCEndpointList
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		403		{object}	responses.ErrorBody
//	@Failure		404		{object}	responses.ErrorBody
//	@Router			/platform/chains/{chainId}/rpc-endpoints [get]
func (c *ChainController) ListRPCEndpoints(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	chainID, err := requests.RouteParam(ctx, "chainId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "chainId is required")
	}
	probe := strings.EqualFold(strings.TrimSpace(ctx.Request().Query("probe")), "true")

	view, err := c.endpoints.List(ctx.Context(), actorID, chainID, probe)
	if err != nil {
		return mapError(ctx, err, "list chain rpc endpoints")
	}

	return ctx.Response().Success().Json(chainsresources.ListFrom(view))
}

// AddRPCEndpoint godoc
//
//	@Summary		Add a chain RPC endpoint
//	@Description	Body {"url", "position"}: an http(s) URL or env:NAME, at the end of the list or at position (1 makes it the primary and moves the others down). The endpoint is first asked which network it serves (eth_chainId on EVM, getGenesisHash on Solana); one on another network, one that does not answer, a URL already in the list or a chain whose adapter does not use the list (Bitcoin family, TRON, XRP) is 422 and nothing is written. The URL is sealed and never returned. The process that answers uses the new list at once; the others load it on restart. An unknown chain is 404 for a platform admin.
//	@Tags			Platform Chains
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			chainId	path		string									true	"Chain id"
//	@Param			body	body		RPCEndpointAddSwagger	true	"Endpoint"
//	@Success		200		{object}	chainsresources.RPCEndpointChange
//	@Failure		400		{object}	responses.ErrorBody
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		403		{object}	responses.ErrorBody
//	@Failure		404		{object}	responses.ErrorBody
//	@Failure		422		{object}	responses.ErrorBody
//	@Router			/platform/chains/{chainId}/rpc-endpoints [post]
func (c *ChainController) AddRPCEndpoint(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	chainID, err := requests.RouteParam(ctx, "chainId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "chainId is required")
	}
	var req chainsrequests.RPCEndpointsRequest
	if err := req.Decode(ctx); err != nil {
		return mapError(ctx, err, "add chain rpc endpoint")
	}

	plan, err := c.endpoints.Add(ctx.Context(), actorID, chainID, req.Fields)
	return endpointChange(ctx, plan, err, "add chain rpc endpoint")
}

// ReplaceRPCEndpoints godoc
//
//	@Summary		Replace a chain's RPC endpoint list
//	@Description	Body {"urls": [...]}: the whole list, the primary first, each an http(s) URL or env:NAME. Every endpoint that was not in the list, and the new primary, is first asked which network it serves; one on another network, one that does not answer, a repeated URL or a chain whose adapter does not use the list is 422 and nothing is written. The URLs are sealed and never returned. An unknown chain is 404 for a platform admin.
//	@Tags			Platform Chains
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			chainId	path		string										true	"Chain id"
//	@Param			body	body		RPCEndpointListSwagger	true	"Endpoints"
//	@Success		200		{object}	chainsresources.RPCEndpointChange
//	@Failure		400		{object}	responses.ErrorBody
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		403		{object}	responses.ErrorBody
//	@Failure		404		{object}	responses.ErrorBody
//	@Failure		422		{object}	responses.ErrorBody
//	@Router			/platform/chains/{chainId}/rpc-endpoints [put]
func (c *ChainController) ReplaceRPCEndpoints(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	chainID, err := requests.RouteParam(ctx, "chainId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "chainId is required")
	}
	var req chainsrequests.RPCEndpointsRequest
	if err := req.Decode(ctx); err != nil {
		return mapError(ctx, err, "replace chain rpc endpoints")
	}

	plan, err := c.endpoints.Replace(ctx.Context(), actorID, chainID, req.Fields)
	return endpointChange(ctx, plan, err, "replace chain rpc endpoints")
}

// ReorderRPCEndpoints godoc
//
//	@Summary		Reorder a chain's RPC endpoints
//	@Description	Body {"order": [2, 1, 3]}: every current position once, in the new order, so the first becomes the primary. A disabled endpoint cannot become the primary, and a new primary is first asked which network it serves; a refusal is 422 and nothing is written. An unknown chain is 404 for a platform admin.
//	@Tags			Platform Chains
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			chainId	path		string										true	"Chain id"
//	@Param			body	body		RPCEndpointOrderSwagger	true	"New order"
//	@Success		200		{object}	chainsresources.RPCEndpointChange
//	@Failure		400		{object}	responses.ErrorBody
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		403		{object}	responses.ErrorBody
//	@Failure		404		{object}	responses.ErrorBody
//	@Failure		422		{object}	responses.ErrorBody
//	@Router			/platform/chains/{chainId}/rpc-endpoints/order [post]
func (c *ChainController) ReorderRPCEndpoints(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	chainID, err := requests.RouteParam(ctx, "chainId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "chainId is required")
	}
	var req chainsrequests.RPCEndpointsRequest
	if err := req.Decode(ctx); err != nil {
		return mapError(ctx, err, "reorder chain rpc endpoints")
	}

	plan, err := c.endpoints.Reorder(ctx.Context(), actorID, chainID, req.Fields)
	return endpointChange(ctx, plan, err, "reorder chain rpc endpoints")
}

// AddDefaultRPCEndpoints godoc
//
//	@Summary		Add the default fallbacks of a chain's network
//	@Description	Appends the checked default fallbacks of the chain's network that the list does not hold yet (ethereum-sepolia: sepolia.drpc.org, sepolia.gateway.tenderly.co, ethereum-sepolia-rpc.publicnode.com), each asked first which network it serves. A network without defaults, or a refusal, is 422 and nothing is written. An unknown chain is 404 for a platform admin.
//	@Tags			Platform Chains
//	@Security		BearerAuth
//	@Produce		json
//	@Param			chainId	path		string	true	"Chain id"
//	@Success		200		{object}	chainsresources.RPCEndpointChange
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		403		{object}	responses.ErrorBody
//	@Failure		404		{object}	responses.ErrorBody
//	@Failure		422		{object}	responses.ErrorBody
//	@Router			/platform/chains/{chainId}/rpc-endpoints/defaults [post]
func (c *ChainController) AddDefaultRPCEndpoints(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	chainID, err := requests.RouteParam(ctx, "chainId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "chainId is required")
	}

	plan, err := c.endpoints.AddDefaults(ctx.Context(), actorID, chainID)
	return endpointChange(ctx, plan, err, "add default chain rpc endpoints")
}

// UpdateRPCEndpoint godoc
//
//	@Summary		Enable or disable a chain RPC fallback
//	@Description	Body {"enabled": bool} for the fallback at position (2 or more): a disabled fallback stays in the list and is not called; one enabled again is first asked which network it serves. The primary (position 1) is always used and is 422. An unknown chain is 404 for a platform admin.
//	@Tags			Platform Chains
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			chainId		path		string										true	"Chain id"
//	@Param			position	path		int											true	"Position in the list"
//	@Param			body		body		RPCEndpointEnabledSwagger	true	"Enabled"
//	@Success		200			{object}	chainsresources.RPCEndpointChange
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		401			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Failure		422			{object}	responses.ErrorBody
//	@Router			/platform/chains/{chainId}/rpc-endpoints/{position} [patch]
func (c *ChainController) UpdateRPCEndpoint(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	chainID, err := requests.RouteParam(ctx, "chainId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "chainId is required")
	}
	position, ok := routePosition(ctx)
	if !ok {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "position must be a positive integer")
	}
	var req chainsrequests.RPCEndpointsRequest
	if err := req.Decode(ctx); err != nil {
		return mapError(ctx, err, "update chain rpc endpoint")
	}

	plan, err := c.endpoints.SetEnabled(ctx.Context(), actorID, chainID, position, req.Fields)
	return endpointChange(ctx, plan, err, "update chain rpc endpoint")
}

// RemoveRPCEndpoint godoc
//
//	@Summary		Remove a chain RPC fallback
//	@Description	Removes the fallback at position (2 or more); the ones after it move up. The primary (position 1) is 422: move another endpoint to position 1 first. An unknown chain is 404 for a platform admin.
//	@Tags			Platform Chains
//	@Security		BearerAuth
//	@Produce		json
//	@Param			chainId		path		string	true	"Chain id"
//	@Param			position	path		int		true	"Position in the list"
//	@Success		200			{object}	chainsresources.RPCEndpointChange
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		401			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Failure		422			{object}	responses.ErrorBody
//	@Router			/platform/chains/{chainId}/rpc-endpoints/{position} [delete]
func (c *ChainController) RemoveRPCEndpoint(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	chainID, err := requests.RouteParam(ctx, "chainId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "chainId is required")
	}
	position, ok := routePosition(ctx)
	if !ok {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "position must be a positive integer")
	}

	plan, err := c.endpoints.Remove(ctx.Context(), actorID, chainID, position)
	return endpointChange(ctx, plan, err, "remove chain rpc endpoint")
}

// routePosition reads {position} as a positive integer.
func routePosition(ctx http.Context) (int, bool) {
	raw, err := requests.RouteParam(ctx, "position")
	if err != nil {
		return 0, false
	}
	position, err := strconv.Atoi(strings.TrimSpace(raw))
	return position, err == nil && position > 0
}

// endpointChange answers a change: the list after it, or the mapped failure.
func endpointChange(ctx http.Context, plan rpcendpoints.Plan, err error, action string) http.Response {
	if err != nil {
		return mapError(ctx, err, action)
	}
	return ctx.Response().Success().Json(chainsresources.ChangeFrom(plan))
}
