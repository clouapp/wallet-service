package chains

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	chainresource "github.com/macrowallets/waas/app/http/resources/chains"
	chainresources "github.com/macrowallets/waas/app/http/resources/dashboard/chains"
	"github.com/macrowallets/waas/app/http/responses"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

// ChainController serves the dashboard chain catalogue.
type ChainController struct {
	chains *chainsvc.Service
}

// NewChainController wires the controller with the chain catalogue service.
func NewChainController(chains *chainsvc.Service) *ChainController {
	if chains == nil {
		panic("dashboard chains controller: chains service is required")
	}
	return &ChainController{chains: chains}
}

// Index godoc
//
//	@Summary		List supported chains
//	@Description	Returns blockchain networks filtered by the account's environment
//	@Tags			Chains
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		SignatureAuth
//	@Success		200	{object}	controllers.ChainListResponse
//	@Failure		500	{object}	responses.ErrorBody
//	@Router			/v1/chains [get]
func (c *ChainController) Index(ctx http.Context) http.Response {
	environment, _ := requestctx.AccountEnvironment(ctx)

	chains, err := c.chains.ListForEnvironment(ctx.Context(), environment)
	if err != nil {
		return mapError(ctx, err, "fetch chains")
	}

	return ctx.Response().Success().Json(http.Json{"data": chainresource.ChainsFrom(chains)})
}

// Show returns a single chain by ID with its tokens and resources.
func (c *ChainController) Show(ctx http.Context) http.Response {
	environment, _ := requestctx.AccountEnvironment(ctx)

	chainID, err := requests.RouteString(ctx, "chainId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "chainId is required")
	}

	detail, err := c.chains.DetailFor(ctx.Context(), chainID, environment)
	if err != nil {
		return mapError(ctx, err, "fetch chain")
	}

	return ctx.Response().Success().Json(chainresources.NewChainDetail(detail))
}

// Tokens returns the tokens of a specific chain.
func (c *ChainController) Tokens(ctx http.Context) http.Response {
	environment, _ := requestctx.AccountEnvironment(ctx)

	chainID, err := requests.RouteString(ctx, "chainId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "chainId is required")
	}

	tokens, err := c.chains.TokensFor(ctx.Context(), chainID, environment)
	if err != nil {
		return mapError(ctx, err, "fetch tokens")
	}

	return ctx.Response().Success().Json(http.Json{"data": chainresources.TokensFrom(tokens)})
}

// Resources returns the resources (explorers, faucets, docs) of a chain.
func (c *ChainController) Resources(ctx http.Context) http.Response {
	environment, _ := requestctx.AccountEnvironment(ctx)

	chainID, err := requests.RouteString(ctx, "chainId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "chainId is required")
	}

	resources, err := c.chains.ResourcesFor(ctx.Context(), chainID, environment)
	if err != nil {
		return mapError(ctx, err, "fetch resources")
	}

	return ctx.Response().Success().Json(http.Json{"data": chainresources.ChainResourcesFrom(resources)})
}
