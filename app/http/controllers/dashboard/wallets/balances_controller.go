package wallets

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
)

// BalancesController serves the dashboard wallet balance route.
type BalancesController struct {
	balances *repositories.WalletAssetBalanceRepository
	tokens   *repositories.TokenRepository
}

func NewBalancesController(
	balances *repositories.WalletAssetBalanceRepository,
	tokens *repositories.TokenRepository,
) *BalancesController {
	if balances == nil {
		panic("dashboard balances controller: balances repository is required")
	}
	if tokens == nil {
		panic("dashboard balances controller: tokens repository is required")
	}
	return &BalancesController{
		balances: balances,
		tokens:   tokens,
	}
}

// ListWalletBalances godoc
// @Summary      List the asset balances of a wallet
// @Description  Returns the native balance and the balances of the tokens the wallet chain configures, as of the last balance refresh. Amounts come raw (base units) and for display, with the asset decimals. Testnet wallets carry no USD price or value.
// @Tags         Wallets
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path  string  true  "Wallet UUID"
// @Success      200  {object}  map[string][]models.WalletAssetBalance
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /wallets/{walletId}/balances [get]
func (ctrl *BalancesController) ListWalletBalances(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)

	rows, err := ctrl.balances.ListByWallet(ctx.Context(), wallet.ID)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch balances"})
	}
	tokens, err := ctrl.tokens.FindByChainID(ctx.Context(), wallet.Chain)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch chain tokens"})
	}

	assets := controllers.PricedConfiguredBalances(ctx.Context(), wallet.Chain, rows, tokens)
	return ctx.Response().Json(http.StatusOK, http.Json{"data": assets})
}
