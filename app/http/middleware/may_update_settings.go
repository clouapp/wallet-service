package middleware

import (
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/policies"
	settingssvc "github.com/macrowallets/waas/app/services/settings"
)

// MayUpdateSettings refuses a dashboard settings write unless the Gate's
// account.update-settings (policies.MayUpdateSettings) allows the account
// role AccountContext already stored. That permission is settings.write.
// The routes are PATCH and PUT /v1/accounts/{accountId}/settings/{group},
// POST /v1/accounts/{accountId}/settings/sections/{section}/cache, and
// POST /v1/accounts/{accountId}/settings/sections/{section}/reset.
// Owner and admin hold it. Auditor and user do not, and the retired viewer
// label stays refused. A denial is 403 with the message the handler returned,
// and nothing is saved, flushed, or reset. An unknown group is left to the
// handler so the answer is 404 before this 403. A section route is left to
// FlushSection and ResetSection, which answer 404 for an unknown page before
// they answer 403.
func MayUpdateSettings() http.Middleware {
	return authorize(facades.Gate(), policies.AbilityAccountUpdateSettings, func(ctx http.Context) (map[string]any, outcome) {
		if strings.TrimSpace(ctx.Request().Route("section")) != "" {
			return nil, pass
		}
		group := strings.TrimSpace(ctx.Request().Route("group"))
		if group != "" && !settingssvc.AccountGroupExists(group) {
			return nil, pass
		}
		return accountRoleArguments(ctx), decide
	})
}
