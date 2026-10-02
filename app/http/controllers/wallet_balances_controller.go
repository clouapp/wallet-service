package controllers

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
)

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
func ListWalletBalances(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)

	rows, err := container.Get().WalletAssetBalanceRepo.ListByWallet(ctx.Context(), wallet.ID)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch balances"})
	}
	tokens, err := container.Get().TokenRepo.FindByChainID(ctx.Context(), wallet.Chain)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch chain tokens"})
	}

	assets := assetBalancesPricedFor(configuredAssetBalances(rows, tokens), resolveWalletChainNetwork(ctx.Context(), wallet.Chain))
	return ctx.Response().Json(http.StatusOK, http.Json{"data": assets})
}
