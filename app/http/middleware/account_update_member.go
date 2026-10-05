package middleware

import (
	"github.com/goravel/framework/contracts/http"

	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// AccountUpdateMember refuses PATCH /v1/accounts/{accountId}/users/{userId}
// unless the account role already stored by AccountContext holds users.write.
// That is the same grant policies.ManagesMembers allows: owner and admin.
// Auditor and user do not hold it. The member is resolved first: a missing
// member is left to the handler, which answers 404, and only a member that
// exists is 403 when users.write is missing. Rank, MayGrant, and MayActOn
// stay in the account service after this check. A denial leaves the
// membership unchanged.
func AccountUpdateMember() http.Middleware {
	return func(ctx http.Context) {
		switch gateAccountChild(ctx) {
		case childPass:
			ctx.Request().Next()
			return
		case childAnswered:
			return
		}
		if !accountPermissionHeld(ctx, PermUsersWrite) {
			abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": accountsvc.ErrManageMembers.Error()})
			return
		}
		ctx.Request().Next()
	}
}
