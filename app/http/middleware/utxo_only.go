package middleware

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/responses"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// UTXOOnly restricts a route to wallets whose chain uses the UTXO model.
func UTXOOnly() http.Middleware {
	return func(ctx http.Context) {
		rawID := ctx.Request().Input("walletId")
		walletID, err := uuid.Parse(rawID)
		if err != nil {
			_ = responses.Send(ctx, http.StatusNotFound, http.Json{"error": "invalid wallet id"}).Abort()
			return
		}

		wallet, err := container.MustMake[*walletrecords.Wallets]().FindByID(ctx.Context(), walletID)
		if err != nil || wallet == nil {
			_ = responses.Send(ctx, http.StatusNotFound, http.Json{"error": "wallet not found"}).Abort()
			return
		}

		if !chainpkg.IsUTXO(wallet.Chain) {
			_ = responses.Send(ctx, http.StatusUnprocessableEntity, http.Json{
				"error": "this endpoint is only available for UTXO-model chains (e.g. bitcoin)",
			}).Abort()
			return
		}

		ctx.Request().Next()
	}
}
