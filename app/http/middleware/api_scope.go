package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/policies"
)

// Permission names a route passes to APIScope. They are the S3.4.6 catalog.
// Routes cannot import policies, so the names are aliased here.
const (
	PermWalletsRead       = policies.PermWalletsRead
	PermWalletsCreate     = policies.PermWalletsCreate
	PermAddressesCreate   = policies.PermAddressesCreate
	PermWithdrawalsCreate = policies.PermWithdrawalsCreate
	PermSweepExecute      = policies.PermSweepExecute
	PermWebhooksRead      = policies.PermWebhooksRead
	PermWebhooksWrite     = policies.PermWebhooksWrite
	PermTransactionsRead  = policies.PermTransactionsRead
)

// APIScope limits a token that lists permissions to those names. A blank
// permissions store keeps today's access. A missing permission is 403
// forbidden: S3.4.6 does not name another code. The check reads the token
// APITokenAuth already stored.
func APIScope(permission string) http.Middleware {
	return func(ctx http.Context) {
		token, ok := requestctx.APIToken(ctx)
		if !ok || token == nil {
			abortWithJSON(ctx, http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
			return
		}
		if !policies.APITokenAllows(token.Permissions, permission) {
			abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": responses.CodeForbidden})
			return
		}
		ctx.Request().Next()
	}
}
