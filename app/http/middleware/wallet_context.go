package middleware

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/responses"
)

// WalletContext resolves the {walletId} route parameter, loads the wallet,
// and verifies the caller is a member of the wallet or its account.
// Sets "wallet" and "wallet_id" in context for downstream handlers.
func WalletContext() http.Middleware {
	return func(ctx http.Context) {
		rawID := ctx.Request().Route("walletId")
		walletID, err := uuid.Parse(rawID)
		if err != nil {
			_ = responses.Send(ctx, http.StatusNotFound, http.Json{"error": "invalid wallet id"}).Abort()
			return
		}

		wallet, err := container.Get().WalletRepo.FindByID(ctx.Context(), walletID)
		if err != nil || wallet == nil {
			_ = responses.Send(ctx, http.StatusNotFound, http.Json{"error": "wallet not found"}).Abort()
			return
		}

		userID := contextUserID(ctx)
		isMember := false

		if wallet.AccountID != nil {
			au, err2 := container.Get().AccountUserRepo.FindByAccountAndUser(ctx.Context(), *wallet.AccountID, userID)
			if err2 == nil && au != nil {
				isMember = true
			}
		}
		if !isMember {
			wu, err3 := container.Get().WalletUserRepo.FindByWalletAndUser(ctx.Context(), walletID, userID)
			if err3 == nil && wu != nil {
				isMember = true
			}
		}

		if !isMember {
			_ = responses.Send(ctx, http.StatusForbidden, http.Json{"error": "not a member of this wallet or its account"}).Abort()
			return
		}

		ctx.WithValue("wallet", wallet)
		ctx.WithValue("wallet_id", wallet.ID)
		ctx.Request().Next()
	}
}
