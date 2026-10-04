package wallets

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// BalancesController serves the dashboard wallet balance route.
type BalancesController struct {
	balances *walletrecords.Balances
	tokens   *chainsvc.Service
}

func NewBalancesController(
	balances *walletrecords.Balances,
	tokens *chainsvc.Service,
) *BalancesController {
	if balances == nil {
		panic("dashboard balances controller: balances service is required")
	}
	if tokens == nil {
		panic("dashboard balances controller: chains service is required")
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
// @Success      200  {object}  map[string][]controllers.WalletAssetBalanceView
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /wallets/{walletId}/balances [get]
func (ctrl *BalancesController) ListWalletBalances(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	rows, err := ctrl.balances.ListByWallet(ctx.Context(), wallet.ID)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch balances"})
	}
	tokens, err := ctrl.tokens.FindTokens(ctx.Context(), wallet.Chain)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch chain tokens"})
	}

	assets := controllers.WalletAssetBalanceViews(controllers.PricedConfiguredBalances(ctx.Context(), wallet.Chain, rows, tokens))
	return responses.Send(ctx, http.StatusOK, http.Json{"data": assets})
}
