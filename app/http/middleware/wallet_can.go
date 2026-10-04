package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/policies"
)

// WalletCan refuses the route unless the account role already stored by
// AccountHeader or AccountContext holds permission on the wallet catalog.
// WalletContext has already loaded the wallet, so a missing wallet is 404
// before this check. The decision is policies.Can. An empty permission and an
// unknown role fail closed. A missing permission is 403 forbidden.
func WalletCan(permission string) http.Middleware {
	return func(ctx http.Context) {
		if !policies.Can(policies.WalletGrants(AccountRole(ctx)), permission) {
			abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": responses.CodeForbidden})
			return
		}
		ctx.Request().Next()
	}
}
