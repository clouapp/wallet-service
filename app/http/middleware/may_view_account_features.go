package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/policies"
)

// MayViewAccountFeatures refuses GET /v1/accounts/{accountId}/features unless
// the Gate's account.view-features (policies.MayViewSettings) allows the
// account role AccountContext already stored. That permission is
// settings.read. Owner, admin, and auditor hold it. User does not, and the
// retired viewer label stays refused. A denial is 403 with the message the
// service returns, and the feature list is not written. List keeps its own
// check.
func MayViewAccountFeatures() http.Middleware {
	return authorize(facades.Gate(), policies.AbilityAccountViewFeatures, func(ctx http.Context) (map[string]any, outcome) {
		return accountRoleArguments(ctx), decide
	})
}
