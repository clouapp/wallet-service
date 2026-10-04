package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/policies"
)

// PermUsersRead is the account member list. Routes cannot import policies.
const PermUsersRead = policies.PermUsersRead

// Can refuses the route unless the account role already stored by
// AccountContext or AccountHeader holds permission. The decision is
// policies.Can on that role's code catalog. An empty permission and an
// unknown role fail closed. A missing permission is 403 forbidden.
func Can(permission string) http.Middleware {
	return func(ctx http.Context) {
		if !policies.Can(policies.AccountRoleGrants(AccountRole(ctx)), permission) {
			abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": responses.CodeForbidden})
			return
		}
		ctx.Request().Next()
	}
}
