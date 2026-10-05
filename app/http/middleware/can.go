package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/policies"
)

// PermUsersRead is the account member list. Routes cannot import policies.
const PermUsersRead = policies.PermUsersRead

// PermUsersWrite is POST and DELETE /v1/accounts/{accountId}/users[/{userId}],
// PATCH /v1/accounts/{accountId}/users/{userId} (AccountUpdateMember), and
// creating an account invite. Routes cannot import policies.
const PermUsersWrite = policies.PermUsersWrite

// PermRolesRead is the account role catalog. Routes cannot import policies.
const PermRolesRead = policies.PermRolesRead

// PermAccountWrite is PATCH /v1/accounts/{accountId}. Routes cannot import policies.
const PermAccountWrite = policies.PermAccountWrite

// PermAccountLifecycle is POST /v1/accounts/{accountId}/freeze and /archive. Routes cannot import policies.
const PermAccountLifecycle = policies.PermAccountLifecycle

// PermTokensRead is GET /v1/accounts/{accountId}/tokens. Routes cannot import policies.
const PermTokensRead = policies.PermTokensRead

// PermTokensWrite is POST /v1/accounts/{accountId}/tokens and DELETE /v1/accounts/{accountId}/tokens/{tokenId}.
// POST also runs MintAPITokenPermissions before the handler. Routes cannot import policies.
const PermTokensWrite = policies.PermTokensWrite

// Can refuses the route unless the account role already stored by
// AccountContext or AccountHeader holds permission. The decision is
// policies.Can on that role's code catalog. An empty permission and an
// unknown role fail closed. A missing permission is 403 forbidden.
func Can(permission string) http.Middleware {
	return func(ctx http.Context) {
		if !accountPermissionHeld(ctx, permission) {
			abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": responses.CodeForbidden})
			return
		}
		ctx.Request().Next()
	}
}

// accountPermissionHeld asks policies.Can with the role AccountContext or
// AccountHeader already stored. An empty permission and an unknown role fail closed.
func accountPermissionHeld(ctx http.Context, permission string) bool {
	grants, ok := policies.AccountGrants(ctx)
	if !ok {
		grants = policies.AccountRoleGrants(AccountRole(ctx))
	}
	return policies.Can(grants, permission)
}
