package chains

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
)

// ChainsController serves the external chain list.
type ChainsController struct {
	chains *repositories.ChainRepository
}

func NewChainsController(
	chains *repositories.ChainRepository,
) *ChainsController {
	if chains == nil {
		panic("external chains controller: chains repository is required")
	}
	return &ChainsController{
		chains: chains,
	}
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
	env, _ := ctx.Value("account_environment").(string)

	var chainList []models.Chain
	var err error
	if env == models.EnvironmentProd || env == models.EnvironmentTest {
		isTestnet := env == models.EnvironmentTest
		chainList, err = ctrl.chains.FindByTestnet(ctx.Context(), isTestnet)
	} else {
		chainList, err = ctrl.chains.FindActive(ctx.Context())
	}
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch chains"})
	}

	return ctx.Response().Success().Json(http.Json{"data": chainList})
}
