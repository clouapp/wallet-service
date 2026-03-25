package middleware

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
)

// AccountContext resolves the {accountId} route parameter, verifies the
// authenticated user is a member of that account, and injects "account" and
// "account_role" into the request context.
// Returns 404 if the account does not exist, 403 if the user is not a member.
func AccountContext(ctx http.Context) {
	rawID := ctx.Request().Input("accountId")
	accountID, err := uuid.Parse(rawID)
	if err != nil {
		ctx.Request().AbortWithStatus(http.StatusNotFound)
		ctx.Response().Json(http.StatusNotFound, http.Json{"error": "invalid account id"})
		return
	}

	accountPtr, err := container.Get().AccountRepo.FindByID(accountID)
	if err != nil || accountPtr == nil {
		ctx.Request().AbortWithStatus(http.StatusNotFound)
		ctx.Response().Json(http.StatusNotFound, http.Json{"error": "account not found"})
		return
	}

	userID := contextUserID(ctx)
	if userID == uuid.Nil {
		ctx.Request().AbortWithStatus(http.StatusUnauthorized)
		ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
		return
	}

	au, err := container.Get().AccountUserRepo.FindByAccountAndUser(accountID, userID)
	if err != nil || au == nil {
		ctx.Request().AbortWithStatus(http.StatusForbidden)
		ctx.Response().Json(http.StatusForbidden, http.Json{"error": "not a member of this account"})
		return
	}

	ctx.WithValue("account", accountPtr)
	ctx.WithValue("account_role", au.Role)
	ctx.Request().Next()
}
