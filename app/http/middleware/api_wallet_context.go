package middleware

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// APIWalletContext verifies that the {walletId} route parameter belongs to the
// account that owns the Bearer API token (injected by APITokenAuth). On any
// mismatch — wallet not found, wallet has no account, or wallet belongs to a
// different account — it returns 404 with a generic "wallet not found" body so
// callers cannot probe for wallet existence across accounts.
//
// On success, the resolved wallet is stored on the request context under the
// same keys used by the dashboard-facing WalletContext middleware so downstream
// controllers can consume it uniformly.
func APIWalletContext(wallets *walletrecords.Wallets) http.Middleware {
	if wallets == nil {
		panic("api wallet context: wallets are required")
	}
	return func(ctx http.Context) {
		walletID, err := uuid.Parse(ctx.Request().Route("walletId"))
		if err != nil {
			_ = responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "wallet not found").Abort()
			return
		}

		accountID, ok := requestctx.AccountID(ctx)
		if !ok || accountID == uuid.Nil {
			_ = responses.Fail(ctx, http.StatusUnauthorized, "unauthenticated", "unauthenticated").Abort()
			return
		}

		wallet, err := wallets.FindByIDAndAccount(ctx.Context(), walletID, accountID)
		if err != nil || wallet == nil {
			_ = responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "wallet not found").Abort()
			return
		}

		ctx.WithValue(requestctx.KeyWallet, wallet)
		ctx.WithValue(requestctx.KeyWalletID, wallet.ID)
		ctx.Request().Next()
	}
}
