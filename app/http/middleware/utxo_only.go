package middleware

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// UTXOOnly restricts a route to wallets whose chain uses the UTXO model.
func UTXOOnly(wallets *walletrecords.Wallets) http.Middleware {
	if wallets == nil {
		panic("utxo only: wallets are required")
	}
	return func(ctx http.Context) {
		rawID := ctx.Request().Input("walletId")
		walletID, err := uuid.Parse(rawID)
		if err != nil {
			_ = responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "invalid wallet id").Abort()
			return
		}

		wallet, err := wallets.FindByID(ctx.Context(), walletID)
		if err != nil || wallet == nil {
			_ = responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "wallet not found").Abort()
			return
		}

		if !chainpkg.IsUTXO(wallet.Chain) {
			_ = responses.Fail(ctx, http.StatusUnprocessableEntity, responses.CodeUnprocessable, "this endpoint is only available for UTXO-model chains (e.g. bitcoin)").Abort()
			return
		}

		ctx.Request().Next()
	}
}
