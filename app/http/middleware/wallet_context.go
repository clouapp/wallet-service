package middleware

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
)

// WalletContext resolves the {walletId} route parameter, loads the wallet,
// and verifies the caller is a member of the wallet or its account.
// Sets "wallet" and "wallet_id" in context for downstream handlers.
func WalletContext() http.Middleware {
	return func(ctx http.Context) {
		rawID := ctx.Request().Route("walletId")
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

		userID := contextUserID(ctx)
		isMember := false

		if wallet.AccountID != nil {
			au, err2 := container.Get().AccountUserRepo.FindByAccountAndUser(*wallet.AccountID, userID)
			if err2 == nil && au != nil {
				isMember = true
			}
		}
		if !isMember {
			wu, err3 := container.Get().WalletUserRepo.FindByWalletAndUser(walletID, userID)
			if err3 == nil && wu != nil {
				isMember = true
			}
		}

		if !isMember {
			ctx.Request().AbortWithStatus(http.StatusForbidden)
			ctx.Response().Json(http.StatusForbidden, http.Json{"error": "not a member of this wallet or its account"})
			return
		}

		ctx.WithValue("wallet", wallet)
		ctx.WithValue("wallet_id", wallet.ID)
		ctx.Request().Next()
	}
}
