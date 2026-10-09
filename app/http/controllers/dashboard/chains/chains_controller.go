package chains

import (
	"errors"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
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

// findChain loads the chain the account environment may use, or returns the
// response that ends the request. A missing chain is a 404 and one of the other
// network kind a 403. Any other repository error is our outage: it is logged and
// answered as the internal error, never as "not found".
func (ctrl *ChainsController) findChain(ctx http.Context, chainID string) (*models.Chain, http.Response) {
	env, _ := requestctx.AccountEnvironment(ctx)
	chain, err := ctrl.chains.FindForEnvironment(ctx.Context(), chainID, env)
	if errors.Is(err, chainsvc.ErrChainNotInEnvironment) {
		return nil, responses.Fail(ctx, http.StatusForbidden, responses.CodeForbidden, err.Error())
	}
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		appfacades.Log().WithContext(ctx).Errorf("chains: find chain %s: %v", chainID, err)
		return nil, responses.InternalError(ctx, nil)
	}
	if chain == nil {
		return nil, responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "chain not found")
	}
	return chain, nil
}

// ListChains godoc
// @Summary      List supported chains
// @Description  Returns blockchain networks filtered by the account's environment
// @Tags         Chains
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Success      200  {object}  controllers.ChainListResponse
// @Failure      500  {object}  responses.ErrorBody
// @Router       /v1/chains [get]
func (ctrl *ChainsController) ListChains(ctx http.Context) http.Response {
	env, _ := requestctx.AccountEnvironment(ctx)

	chainList, err := ctrl.chains.ListForEnvironment(ctx.Context(), env)
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to fetch chains")
	}

	return ctx.Response().Success().Json(http.Json{"data": chainresource.ChainsFrom(chainList)})
}

// GetChain returns a single chain by ID with its tokens and resources.
func (ctrl *ChainsController) GetChain(ctx http.Context) http.Response {
	chainID := ctx.Request().Route("chainId")
	if chainID == "" {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "chainId is required")
	}

	chain, failure := ctrl.findChain(ctx, chainID)
	if failure != nil {
		return failure
	}

	// A failed read leaves the list empty rather than failing the chain view.
	tokens, err := ctrl.chains.FindTokens(ctx.Context(), chainID)
	if err != nil {
		slog.Warn("chains: load tokens for chain view", "chain", chainID, "error", err)
	}
	resources, err := ctrl.chains.FindResources(ctx.Context(), chainID)
	if err != nil {
		slog.Warn("chains: load resources for chain view", "chain", chainID, "error", err)
	}

	return ctx.Response().Success().Json(http.Json{
		"chain":     chainresource.ChainPtr(chain),
		"tokens":    chainresources.TokensFrom(tokens),
		"resources": chainresources.ChainResourcesFrom(resources),
	})
}

// ListChainTokens returns tokens for a specific chain.
func (ctrl *ChainsController) ListChainTokens(ctx http.Context) http.Response {
	chainID := ctx.Request().Route("chainId")
	if chainID == "" {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "chainId is required")
	}

	_, failure := ctrl.findChain(ctx, chainID)
	if failure != nil {
		return failure
	}

	tokens, tokenErr := ctrl.chains.FindTokens(ctx.Context(), chainID)
	if tokenErr != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to fetch tokens")
	}

	return ctx.Response().Success().Json(http.Json{"data": chainresources.TokensFrom(tokens)})
}

// ListChainResources returns resources (explorers, faucets, docs) for a chain.
func (ctrl *ChainsController) ListChainResources(ctx http.Context) http.Response {
	chainID := ctx.Request().Route("chainId")
	if chainID == "" {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "chainId is required")
	}

	_, failure := ctrl.findChain(ctx, chainID)
	if failure != nil {
		return failure
	}

	resources, resErr := ctrl.chains.FindResources(ctx.Context(), chainID)
	if resErr != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to fetch resources")
	}

	return ctx.Response().Success().Json(http.Json{"data": chainresources.ChainResourcesFrom(resources)})
}
