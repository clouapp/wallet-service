package chains

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	chainresource "github.com/macrowallets/waas/app/http/resources/chains"
	chainresources "github.com/macrowallets/waas/app/http/resources/dashboard/chains"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

// ChainsController serves the dashboard chain catalogue.
type ChainsController struct {
	chains *chainsvc.Service
}

func NewChainsController(chains *chainsvc.Service) *ChainsController {
	if chains == nil {
		panic("dashboard chains controller: chains service is required")
	}
	return &ChainsController{chains: chains}
}

// ListChains godoc
// @Summary      List supported chains
// @Description  Returns blockchain networks filtered by the account's environment
// @Tags         Chains
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Success      200  {object}  ChainListResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /v1/chains [get]
func (ctrl *ChainsController) ListChains(ctx http.Context) http.Response {
	env, _ := requestctx.AccountEnvironment(ctx)

	chainList, err := ctrl.chains.ListForEnvironment(ctx.Context(), env)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch chains"})
	}

	return ctx.Response().Success().Json(http.Json{"data": chainresource.ChainsFrom(chainList)})
}

// GetChain returns a single chain by ID with its tokens and resources.
func (ctrl *ChainsController) GetChain(ctx http.Context) http.Response {
	var path requests.ChainIDRequest
	path.Load(ctx)
	chainID := path.ChainID
	if chainID == "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "chainId is required"})
	}

	chain, err := ctrl.chains.FindByID(ctx.Context(), chainID)
	if err != nil || chain == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "chain not found"})
	}

	env, _ := requestctx.AccountEnvironment(ctx)
	if env == models.EnvironmentProd || env == models.EnvironmentTest {
		isTestnet := env == models.EnvironmentTest
		if chain.IsTestnet != isTestnet {
			return responses.Send(ctx, http.StatusForbidden, http.Json{"error": "chain not available in current environment"})
		}
	}

	tokens, _ := ctrl.chains.FindTokens(ctx.Context(), chainID)
	resources, _ := ctrl.chains.FindResources(ctx.Context(), chainID)

	return ctx.Response().Success().Json(http.Json{
		"chain":     chainresource.ChainPtr(chain),
		"tokens":    chainresources.TokensFrom(tokens),
		"resources": chainresources.ChainResourcesFrom(resources),
	})
}

// ListChainTokens returns tokens for a specific chain.
func (ctrl *ChainsController) ListChainTokens(ctx http.Context) http.Response {
	var path requests.ChainIDRequest
	path.Load(ctx)
	chainID := path.ChainID
	if chainID == "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "chainId is required"})
	}

	chain, err := ctrl.chains.FindByID(ctx.Context(), chainID)
	if err != nil || chain == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "chain not found"})
	}

	env, _ := requestctx.AccountEnvironment(ctx)
	if env == models.EnvironmentProd || env == models.EnvironmentTest {
		isTestnet := env == models.EnvironmentTest
		if chain.IsTestnet != isTestnet {
			return responses.Send(ctx, http.StatusForbidden, http.Json{"error": "chain not available in current environment"})
		}
	}

	tokens, tokenErr := ctrl.chains.FindTokens(ctx.Context(), chainID)
	if tokenErr != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch tokens"})
	}

	return ctx.Response().Success().Json(http.Json{"data": chainresources.TokensFrom(tokens)})
}

// ListChainResources returns resources (explorers, faucets, docs) for a chain.
func (ctrl *ChainsController) ListChainResources(ctx http.Context) http.Response {
	var path requests.ChainIDRequest
	path.Load(ctx)
	chainID := path.ChainID
	if chainID == "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "chainId is required"})
	}

	chain, err := ctrl.chains.FindByID(ctx.Context(), chainID)
	if err != nil || chain == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "chain not found"})
	}

	env, _ := requestctx.AccountEnvironment(ctx)
	if env == models.EnvironmentProd || env == models.EnvironmentTest {
		isTestnet := env == models.EnvironmentTest
		if chain.IsTestnet != isTestnet {
			return responses.Send(ctx, http.StatusForbidden, http.Json{"error": "chain not available in current environment"})
		}
	}

	resources, resErr := ctrl.chains.FindResources(ctx.Context(), chainID)
	if resErr != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch resources"})
	}

	return ctx.Response().Success().Json(http.Json{"data": chainresources.ChainResourcesFrom(resources)})
}
