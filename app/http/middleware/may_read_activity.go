package middleware

import (
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/policies"
)

// MayReadActivity refuses GET /v1/accounts/{accountId}/activity unless the
// Gate's account.read-activity (policies.MayReadActivity) allows the account
// role AccountContext already stored. That permission is activity.read.
// Owner, admin, and auditor hold it. User does not, and the retired viewer
// label stays refused. A denial is 403 with the message the handler
// returned, and the activity list is not written.
// GET /v1/accounts/{accountId}/activity/{id} resolves the row in the handler
// first: a missing id is 404, and activity.read is asked only after that row
// is found.
func MayReadActivity() http.Middleware {
	return authorize(facades.Gate(), policies.AbilityAccountReadActivity, func(ctx http.Context) (map[string]any, outcome) {
		if strings.TrimSpace(ctx.Request().Route("id")) != "" {
			return nil, pass
		}
		return accountRoleArguments(ctx), decide
	})
}
