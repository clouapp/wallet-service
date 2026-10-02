package middleware

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/responses"
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
func APIWalletContext() http.Middleware {
	return func(ctx http.Context) {
		walletID, err := uuid.Parse(ctx.Request().Route("walletId"))
		if err != nil {
			abortWithJSON(ctx, http.StatusNotFound, http.Json{"error": "wallet not found"})
			return
		}

		accountID, ok := ctx.Value("account_id").(uuid.UUID)
		if !ok || accountID == uuid.Nil {
			abortWithJSON(ctx, http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
			return
		}

		wallet, err := container.Get().WalletRepo.FindByIDAndAccount(walletID, accountID)
		if err != nil || wallet == nil {
			abortWithJSON(ctx, http.StatusNotFound, http.Json{"error": "wallet not found"})
			return
		}

		ctx.WithValue("wallet", wallet)
		ctx.WithValue("wallet_id", wallet.ID)
		ctx.Request().Next()
	}
}

// abortWithJSON short-circuits the middleware chain with a JSON body. Using
// Response().Json(...).Abort() is the non-deprecated pattern in Goravel v1.17
// and ensures the body is actually written before the abort takes effect.
func abortWithJSON(ctx http.Context, code int, body http.Json) {
	_ = responses.Send(ctx, code, body).Abort()
}
