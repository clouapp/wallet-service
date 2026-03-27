package controllers

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
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
		chainList, err = container.Get().ChainRepo.FindByTestnet(isTestnet)
	} else {
		chainList, err = container.Get().ChainRepo.FindActive()
	}
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch chains"})
	}

	return ctx.Response().Success().Json(http.Json{"data": chainList})
}

// GetChain returns a single chain by ID with its tokens and resources.
func GetChain(ctx http.Context) http.Response {
	chainID := ctx.Request().Input("chainId")
	if chainID == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "chainId is required"})
	}

	chain, err := container.Get().ChainRepo.FindByID(chainID)
	if err != nil || chain == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "chain not found"})
	}

	env, _ := ctx.Value("account_environment").(string)
	if env == models.EnvironmentProd || env == models.EnvironmentTest {
		isTestnet := env == models.EnvironmentTest
		if chain.IsTestnet != isTestnet {
			return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "chain not available in current environment"})
		}
	}

	tokens, _ := container.Get().TokenRepo.FindByChainID(chainID)
	resources, _ := container.Get().ChainResourceRepo.FindByChainID(chainID)

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
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "chainId is required"})
	}

	chain, err := container.Get().ChainRepo.FindByID(chainID)
	if err != nil || chain == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "chain not found"})
	}

	env, _ := ctx.Value("account_environment").(string)
	if env == models.EnvironmentProd || env == models.EnvironmentTest {
		isTestnet := env == models.EnvironmentTest
		if chain.IsTestnet != isTestnet {
			return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "chain not available in current environment"})
		}
	}

	tokens, tokenErr := container.Get().TokenRepo.FindByChainID(chainID)
	if tokenErr != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch tokens"})
	}

	return ctx.Response().Success().Json(http.Json{"data": tokens})
}

// ListChainResources returns resources (explorers, faucets, docs) for a chain.
func ListChainResources(ctx http.Context) http.Response {
	chainID := ctx.Request().Input("chainId")
	if chainID == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "chainId is required"})
	}

	chain, err := container.Get().ChainRepo.FindByID(chainID)
	if err != nil || chain == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "chain not found"})
	}

	env, _ := ctx.Value("account_environment").(string)
	if env == models.EnvironmentProd || env == models.EnvironmentTest {
		isTestnet := env == models.EnvironmentTest
		if chain.IsTestnet != isTestnet {
			return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "chain not available in current environment"})
		}
	}

	resources, resErr := container.Get().ChainResourceRepo.FindByChainID(chainID)
	if resErr != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch resources"})
	}

	return ctx.Response().Success().Json(http.Json{"data": resources})
}
