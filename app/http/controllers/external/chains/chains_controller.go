package chains

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	chainresource "github.com/macrowallets/waas/app/http/resources/chains"
	"github.com/macrowallets/waas/app/http/responses"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

// ChainsController serves the external chain list.
type ChainsController struct {
	chains *chainsvc.Service
}

func NewChainsController(chains *chainsvc.Service) *ChainsController {
	if chains == nil {
		panic("external chains controller: chains service is required")
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
// @Success      200  {object}  controllers.ChainListResponse
// @Failure      500  {object}  responses.ErrorBody
// @Failure      429  {object}  responses.ErrorBody  "Rate limit exceeded (too_many_requests, Retry-After header)"
// @Router       /v1/chains [get]
func (ctrl *ChainsController) ListChains(ctx http.Context) http.Response {
	env, _ := requestctx.AccountEnvironment(ctx)

	chainList, err := ctrl.chains.ListForEnvironment(ctx.Context(), env)
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to fetch chains")
	}

	return ctx.Response().Success().Json(http.Json{"data": chainresource.ChainsFrom(chainList)})
}
