package controllers

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
)

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
func ListChains(ctx http.Context) http.Response {
	env, _ := ctx.Value("account_environment").(string)

	var chainList []models.Chain
	var err error
	if env == models.EnvironmentProd || env == models.EnvironmentTest {
		isTestnet := env == models.EnvironmentTest
		chainList, err = container.MustMake[*repositories.ChainRepository]().FindByTestnet(ctx.Context(), isTestnet)
	} else {
		chainList, err = container.MustMake[*repositories.ChainRepository]().FindActive(ctx.Context())
	}
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch chains"})
	}

	return ctx.Response().Success().Json(http.Json{"data": chainList})
}

// GetChain returns a single chain by ID with its tokens and resources.
func GetChain(ctx http.Context) http.Response {
	chainID := ctx.Request().Input("chainId")
	if chainID == "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "chainId is required"})
	}

	chain, err := container.MustMake[*repositories.ChainRepository]().FindByID(ctx.Context(), chainID)
	if err != nil || chain == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "chain not found"})
	}

	env, _ := ctx.Value("account_environment").(string)
	if env == models.EnvironmentProd || env == models.EnvironmentTest {
		isTestnet := env == models.EnvironmentTest
		if chain.IsTestnet != isTestnet {
			return responses.Send(ctx, http.StatusForbidden, http.Json{"error": "chain not available in current environment"})
		}
	}

	tokens, _ := container.MustMake[*repositories.TokenRepository]().FindByChainID(ctx.Context(), chainID)
	resources, _ := container.MustMake[*repositories.ChainResourceRepository]().FindByChainID(ctx.Context(), chainID)

	return ctx.Response().Success().Json(http.Json{
		"chain":     chain,
		"tokens":    tokens,
		"resources": resources,
	})
}

// ListChainTokens returns tokens for a specific chain.
func ListChainTokens(ctx http.Context) http.Response {
	chainID := ctx.Request().Input("chainId")
	if chainID == "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "chainId is required"})
	}

	chain, err := container.MustMake[*repositories.ChainRepository]().FindByID(ctx.Context(), chainID)
	if err != nil || chain == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "chain not found"})
	}

	env, _ := ctx.Value("account_environment").(string)
	if env == models.EnvironmentProd || env == models.EnvironmentTest {
		isTestnet := env == models.EnvironmentTest
		if chain.IsTestnet != isTestnet {
			return responses.Send(ctx, http.StatusForbidden, http.Json{"error": "chain not available in current environment"})
		}
	}

	tokens, tokenErr := container.MustMake[*repositories.TokenRepository]().FindByChainID(ctx.Context(), chainID)
	if tokenErr != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch tokens"})
	}

	return ctx.Response().Success().Json(http.Json{"data": tokens})
}

// ListChainResources returns resources (explorers, faucets, docs) for a chain.
func ListChainResources(ctx http.Context) http.Response {
	chainID := ctx.Request().Input("chainId")
	if chainID == "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "chainId is required"})
	}

	chain, err := container.MustMake[*repositories.ChainRepository]().FindByID(ctx.Context(), chainID)
	if err != nil || chain == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "chain not found"})
	}

	env, _ := ctx.Value("account_environment").(string)
	if env == models.EnvironmentProd || env == models.EnvironmentTest {
		isTestnet := env == models.EnvironmentTest
		if chain.IsTestnet != isTestnet {
			return responses.Send(ctx, http.StatusForbidden, http.Json{"error": "chain not available in current environment"})
		}
	}

	resources, resErr := container.MustMake[*repositories.ChainResourceRepository]().FindByChainID(ctx.Context(), chainID)
	if resErr != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch resources"})
	}

	return ctx.Response().Success().Json(http.Json{"data": resources})
}
