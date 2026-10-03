package middleware

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
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

		accountPtr, err := container.Get().AccountRepo.FindByID(ctx.Context(), accountID)
		if err != nil || accountPtr == nil {
			_ = responses.Send(ctx, http.StatusNotFound, http.Json{"error": "account not found"}).Abort()
			return
		}

		userID := contextUserID(ctx)
		if userID == uuid.Nil {
			_ = responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthenticated"}).Abort()
			return
		}

		au, err := container.Get().AccountUserRepo.FindByAccountAndUser(ctx.Context(), accountID, userID)
		if err != nil || au == nil || !models.MembershipGrantsAccess(au.Status) {
			_ = responses.Send(ctx, http.StatusForbidden, http.Json{"error": "not a member of this account"}).Abort()
			return
		}

		ctx.WithValue("account", accountPtr)
		ctx.WithValue("account_role", au.Role)
		ctx.Request().Next()
	}
}

// AccountFrom is the account AccountContext stored. A missing value is nil.
func AccountFrom(ctx http.Context) *models.Account {
	if ctx == nil {
		return nil
	}
	account, _ := ctx.Value("account").(*models.Account)
	return account
}

// AccountRole is the caller's account_users.role stored by AccountContext.
// A missing value is empty, which denies every rank check.
func AccountRole(ctx http.Context) string {
	if ctx == nil {
		return ""
	}
	role, _ := ctx.Value("account_role").(string)
	return role
}
