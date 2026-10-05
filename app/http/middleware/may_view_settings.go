package middleware

import (
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/policies"
	settingssvc "github.com/macrowallets/waas/app/services/settings"
)

// MayViewSettings refuses a dashboard settings read unless
// policies.MayViewSettings allows the account role AccountContext already
// stored. That permission is settings.read. The routes are
// GET /v1/accounts/{accountId}/settings and
// GET /v1/accounts/{accountId}/settings/{group}. Owner, admin, and auditor
// hold it. User does not, and the retired viewer label stays refused. A denial
// is 403 with the message the handler returned, and the settings body is
// not written. An unknown group, including a platform-only name, is left
// to the handler so the answer is 404 before this 403. Flush and reset
// keep their own checks.
func MayViewSettings() http.Middleware {
	return func(ctx http.Context) {
		group := strings.TrimSpace(ctx.Request().Route("group"))
		if group != "" && !settingssvc.AccountGroupExists(group) {
			ctx.Request().Next()
			return
		}
		if !policies.MayViewSettings(AccountRole(ctx)) {
			abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": settingssvc.ErrViewForbidden.Error()})
			return
		}
		ctx.Request().Next()
	}
}
