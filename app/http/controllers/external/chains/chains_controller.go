package chains

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
