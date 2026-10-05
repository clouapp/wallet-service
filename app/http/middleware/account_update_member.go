package middleware

import (
	"github.com/goravel/framework/contracts/http"

	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// AccountUpdateMember refuses PATCH /v1/accounts/{accountId}/users/{userId}
// unless the account role already stored by AccountContext holds users.write.
// That is the same grant policies.ManagesMembers allows: owner and admin.
// Auditor and user do not hold it. A denial is 403 with the message the
// handler returned, and the membership is left unchanged. Rank, MayGrant,
// and MayActOn stay in the account service after this check.
func AccountUpdateMember() http.Middleware {
	return func(ctx http.Context) {
		if !accountPermissionHeld(ctx, PermUsersWrite) {
			abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": accountsvc.ErrManageMembers.Error()})
			return
		}
		ctx.Request().Next()
	}
}
