package balances

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	walletresource "github.com/macrowallets/waas/app/http/resources/dashboard/wallets"
	walletbalances "github.com/macrowallets/waas/app/http/resources/dashboard/wallets/balances"
	"github.com/macrowallets/waas/app/services/walletview"
)

// BalanceController serves the dashboard wallet balance route.
type BalanceController struct {
	view *walletview.Service
}

// NewBalanceController wires the controller with the wallet reads.
func NewBalanceController(view *walletview.Service) *BalanceController {
	if view == nil {
		panic("dashboard balances controller: wallet view is required")
	}
	return &BalanceController{view: view}
}

// Index godoc
//
//	@Summary		List the asset balances of a wallet
//	@Description	Returns the native balance and the balances of the tokens the wallet chain configures, as of the last balance refresh. Amounts come raw (base units) and for display, with the asset decimals. Testnet wallets carry no USD price or value.
//	@Tags			Wallets
//	@Security		BearerAuth
//	@Produce		json
//	@Param			walletId	path		string	true	"Wallet UUID"
//	@Success		200			{object}	map[string][]walletbalances.Balance
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/wallets/{walletId}/balances [get]
func (c *BalanceController) Index(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	balances, err := c.view.Balances(ctx.Context(), wallet)
	if err != nil {
		return mapError(ctx, err, "fetch balances")
	}

	return ctx.Response().Success().Json(http.Json{"data": walletbalances.BalancesFrom(balances, walletresource.WalletPtr)})
}
