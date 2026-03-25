package middleware

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
)

// UTXOOnly restricts a route to wallets whose chain is a UTXO-model chain
// (e.g. bitcoin). Returns 422 Unprocessable Entity for account-model wallets.
// Requires the {walletId} route parameter and a prior SessionAuth middleware.
func UTXOOnly(ctx http.Context) {
	rawID := ctx.Request().Input("walletId")
	walletID, err := uuid.Parse(rawID)
	if err != nil {
		ctx.Request().AbortWithStatus(http.StatusNotFound)
		ctx.Response().Json(http.StatusNotFound, http.Json{"error": "invalid wallet id"})
		return
	}

	wallet, err := container.Get().WalletRepo.FindByID(walletID)
	if err != nil || wallet == nil {
		ctx.Request().AbortWithStatus(http.StatusNotFound)
		ctx.Response().Json(http.StatusNotFound, http.Json{"error": "wallet not found"})
		return
	}

	if !chainpkg.IsUTXO(wallet.Chain) {
		ctx.Request().AbortWithStatus(http.StatusUnprocessableEntity)
		ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{
			"error": "this endpoint is only available for UTXO-model chains (e.g. bitcoin)",
		})
		return
	}

	ctx.Request().Next()
}
