package middleware

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macromarkets/vault/app/models"
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

	var account models.Account
	if err := facades.Orm().Query().Where("id = ?", accountID).First(&account); err != nil {
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

	var au models.AccountUser
	if err := facades.Orm().Query().
		Where("account_id = ? AND user_id = ? AND deleted_at IS NULL", accountID, userID).
		First(&au); err != nil {
		ctx.Request().AbortWithStatus(http.StatusForbidden)
		ctx.Response().Json(http.StatusForbidden, http.Json{"error": "not a member of this account"})
		return
	}

	ctx.WithValue("account", &account)
	ctx.WithValue("account_role", au.Role)
	ctx.Request().Next()
}
