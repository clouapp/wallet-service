package middleware

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
)

// AccountHeader reads X-Account-Id from the request header, validates the
// authenticated user is a member of that account, and injects account context
// values ("account", "account_id", "account_role", "account_environment").
// Used for routes like /v1/wallets and /v1/chains where account ID comes
// from a header rather than a route parameter.
func AccountHeader(ctx http.Context) {
	rawID := ctx.Request().Header("X-Account-Id")
	if rawID == "" {
		ctx.Request().AbortWithStatus(http.StatusBadRequest)
		ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "X-Account-Id header is required"})
		return
	}

	accountID, err := uuid.Parse(rawID)
	if err != nil {
		ctx.Request().AbortWithStatus(http.StatusBadRequest)
		ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid X-Account-Id"})
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
	ctx.WithValue("account_id", accountID)
	ctx.WithValue("account_role", au.Role)
	ctx.WithValue("account_environment", accountPtr.Environment)
	ctx.Request().Next()
}
