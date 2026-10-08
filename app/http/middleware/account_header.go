package middleware

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/packages/activitylog"
)

// AccountHeader reads X-Account-Id from the request header, validates the
// authenticated user is a member of that account, and injects account context values.
func AccountHeader(accounts accountScope) http.Middleware {
	if accounts == nil {
		panic("account header: account service is required")
	}
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

		accountPtr, err := accounts.FindByID(ctx.Context(), accountID)
		if err != nil || accountPtr == nil {
			_ = responses.Send(ctx, http.StatusNotFound, http.Json{"error": "account not found"}).Abort()
			return
		}

		userID := contextUserID(ctx)
		if userID == uuid.Nil {
			_ = responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthenticated"}).Abort()
			return
		}

		au, err := accounts.FindMember(ctx.Context(), accountID, userID)
		if err != nil || au == nil || !models.MembershipGrantsAccess(au.Status) {
			_ = responses.Send(ctx, http.StatusForbidden, http.Json{"error": "not a member of this account"}).Abort()
			return
		}

		if !abortUnlessAccountAllows(ctx, accountPtr) {
			return
		}

		ctx.WithValue(requestctx.KeyAccount, accountPtr)
		ctx.WithValue(requestctx.KeyAccountID, accountID)
		ctx.WithValue(requestctx.KeyAccountRole, au.Role)
		ctx.WithValue(policies.RequestGrantsKey(), policies.AttachRequestGrants(accountID, userID, au.Role))
		ctx.WithValue(requestctx.KeyAccountEnvironment, accountPtr.Environment)
		ctx.WithContext(activitylog.WithScope(ctx.Context(), "account:"+accountID.String()))
		ctx.Request().Next()
	}
}
