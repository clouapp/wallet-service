package middleware

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
)

// AccountHeader reads X-Account-Id from the request header, validates the
// authenticated user is a member of that account, and injects account context values.
func AccountHeader() http.Middleware {
	return func(ctx http.Context) {
		rawID := ctx.Request().Header("X-Account-Id")
		if rawID == "" {
			_ = responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "X-Account-Id header is required"}).Abort()
			return
		}

		accountID, err := uuid.Parse(rawID)
		if err != nil {
			_ = responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid X-Account-Id"}).Abort()
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
		ctx.WithValue("account_id", accountID)
		ctx.WithValue("account_role", au.Role)
		ctx.WithValue("account_environment", accountPtr.Environment)
		ctx.Request().Next()
	}
}
