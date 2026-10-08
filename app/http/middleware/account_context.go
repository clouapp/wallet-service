package middleware

import (
	"context"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/packages/activitylog"
)

// accountScope is the account and membership lookup AccountContext and AccountHeader need.
type accountScope interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.Account, error)
	FindMember(ctx context.Context, accountID, userID uuid.UUID) (*models.AccountUser, error)
}

// AccountContext resolves the {accountId} route parameter and verifies membership.
func AccountContext(accounts accountScope) http.Middleware {
	if accounts == nil {
		panic("account context: account service is required")
	}
	return func(ctx http.Context) {
		rawID := ctx.Request().Input("accountId")
		accountID, err := uuid.Parse(rawID)
		if err != nil {
			_ = responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "invalid account id").Abort()
			return
		}

		accountPtr, err := accounts.FindByID(ctx.Context(), accountID)
		if err != nil || accountPtr == nil {
			_ = responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "account not found").Abort()
			return
		}

		userID := contextUserID(ctx)
		if userID == uuid.Nil {
			_ = responses.Fail(ctx, http.StatusUnauthorized, "unauthenticated", "unauthenticated").Abort()
			return
		}

		au, err := accounts.FindMember(ctx.Context(), accountID, userID)
		if err != nil || au == nil || !models.MembershipGrantsAccess(au.Status) {
			_ = responses.Fail(ctx, http.StatusForbidden, responses.CodeForbidden, "not a member of this account").Abort()
			return
		}

		if !abortUnlessAccountAllows(ctx, accountPtr) {
			return
		}

		ctx.WithValue(requestctx.KeyAccount, accountPtr)
		ctx.WithValue(requestctx.KeyAccountRole, au.Role)
		ctx.WithValue(policies.RequestGrantsKey(), policies.AttachRequestGrants(au.Role))
		ctx.WithContext(activitylog.WithScope(ctx.Context(), "account:"+accountID.String()))
		ctx.Request().Next()
	}
}

// AccountFrom is the account AccountContext stored. A missing value is nil.
func AccountFrom(ctx http.Context) *models.Account {
	if ctx == nil {
		return nil
	}
	account, _ := requestctx.Account(ctx)
	return account
}

// AccountRole is the caller's account_users.role stored by AccountContext.
// A missing value is empty, which denies every rank check.
func AccountRole(ctx http.Context) string {
	if ctx == nil {
		return ""
	}
	role, _ := requestctx.AccountRole(ctx)
	return role
}
