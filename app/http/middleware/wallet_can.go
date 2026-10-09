package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/policies"
)

// WalletCan refuses the route unless the account role already stored by
// AccountHeader or AccountContext holds permission on the wallet catalog.
// WalletContext has already loaded the wallet, so a missing wallet is 404
// before this check. The Gate's ability for permission decides with
// policies.Can over the stored request grants, or the role's wallet catalog
// when none were stored. An unknown role fails closed, and a permission the
// Gate does not define is refused when the route is built. A missing
// permission is 403 forbidden.
func WalletCan(permission string) http.Middleware {
	return authorize(facades.Gate(), permission, func(ctx http.Context) (map[string]any, outcome) {
		grants, ok := policies.WalletRequestGrants(ctx)
		if !ok {
			grants = policies.WalletGrants(AccountRole(ctx))
		}
		return map[string]any{policies.ArgGrants: grants}, decide
	})
}
