package chains

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	chainresource "github.com/macrowallets/waas/app/http/resources/chains"
	"github.com/macrowallets/waas/app/http/responses"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

// ChainController serves the external chain list and the tokens of a chain.
type ChainController struct {
	chains *chainsvc.Service
}

// NewChainController wires the controller with the chain catalogue service.
func NewChainController(chains *chainsvc.Service) *ChainController {
	if chains == nil {
		panic("external chains controller: chains service is required")
	}
	return &ChainController{chains: chains}
}

// Index godoc
//
//	@Summary		List supported chains
//	@Description	Returns blockchain networks filtered by the account's environment. finality is the rule that confirms a chain's transactions: confirmations (status follows confirmations against required_confirmations), finalized_tag (EVM: confirmed once the block is at or below the node's finalized block) or solidified (TRON: at or below the solidified block). Under finalized_tag and solidified, required_confirmations is the typical finality lag, kept for "n/N confirmations" displays; the status is decided by the policy.
//	@Tags			Chains
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		SignatureAuth
//	@Success		200	{object}	controllers.ChainListResponse
//	@Failure		500	{object}	responses.ErrorBody
//	@Failure		429	{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/v1/chains [get]
func (c *ChainController) Index(ctx http.Context) http.Response {
	environment, _ := requestctx.AccountEnvironment(ctx)

	chains, err := c.chains.ListForEnvironment(ctx.Context(), environment)
	if err != nil {
		return mapError(ctx, err, "fetch chains")
	}

	return ctx.Response().Success().Json(http.Json{"data": chainresource.ChainsFrom(chains)})
}

// Tokens godoc
//
//	@Summary		List the tokens of a chain
//	@Description	Returns the active tokens the catalogue lists for the chain, the same rows as the dashboard's GET /v1/chains/{chainId}/tokens. The chain must belong to the account's environment: a chain of the other network kind is 403, an unknown chain 404.
//	@Tags			Chains
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		SignatureAuth
//	@Param			chainId	path		string	true	"Chain id"
//	@Success		200		{object}	controllers.ChainTokenListResponse
//	@Failure		403		{object}	responses.ErrorBody	"Chain of the other environment (forbidden)"
//	@Failure		404		{object}	responses.ErrorBody	"Unknown chain (not_found)"
//	@Failure		500		{object}	responses.ErrorBody
//	@Failure		429		{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/api/v1/chains/{chainId}/tokens [get]
func (c *ChainController) Tokens(ctx http.Context) http.Response {
	environment, _ := requestctx.AccountEnvironment(ctx)

	chainID, err := requests.RouteParam(ctx, "chainId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "chainId is required")
	}

	tokens, err := c.chains.TokensFor(ctx.Context(), chainID, environment)
	if err != nil {
		return mapError(ctx, err, "fetch tokens")
	}

	return ctx.Response().Success().Json(http.Json{"data": chainresource.TokensFrom(tokens)})
}
