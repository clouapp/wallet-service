package middleware

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/responses"
)

// AccountContext resolves the {accountId} route parameter and verifies membership.
func AccountContext() http.Middleware {
	return func(ctx http.Context) {
		rawID := ctx.Request().Input("accountId")
		accountID, err := uuid.Parse(rawID)
		if err != nil {
			_ = responses.Send(ctx, http.StatusNotFound, http.Json{"error": "invalid account id"}).Abort()
			return
		}

		accountPtr, err := container.Get().AccountRepo.FindByID(accountID)
		if err != nil || accountPtr == nil {
			_ = responses.Send(ctx, http.StatusNotFound, http.Json{"error": "account not found"}).Abort()
			return
		}

		userID := contextUserID(ctx)
		if userID == uuid.Nil {
			_ = responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthenticated"}).Abort()
			return
		}

		au, err := container.Get().AccountUserRepo.FindByAccountAndUser(accountID, userID)
		if err != nil || au == nil {
			_ = responses.Send(ctx, http.StatusForbidden, http.Json{"error": "not a member of this account"}).Abort()
			return
		}

		ctx.WithValue("account", accountPtr)
		ctx.WithValue("account_role", au.Role)
		ctx.Request().Next()
	}
}
