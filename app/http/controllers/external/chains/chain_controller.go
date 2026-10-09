package chains

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	chainresource "github.com/macrowallets/waas/app/http/resources/chains"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

// ChainController serves the external chain list.
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
//	@Description	Returns blockchain networks filtered by the account's environment
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
