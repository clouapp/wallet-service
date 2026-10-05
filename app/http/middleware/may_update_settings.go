package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/policies"
	settingssvc "github.com/macrowallets/waas/app/services/settings"
)

// MayUpdateSettings refuses a dashboard settings write unless
// policies.MayUpdateSettings allows the account role AccountContext already
// stored. That permission is settings.write. The routes are
// PATCH and PUT /v1/accounts/{accountId}/settings/{group} and
// POST /v1/accounts/{accountId}/settings/sections/{section}/cache.
// Owner and admin hold it. Auditor and user do not, and the retired viewer
// label stays refused. A denial is 403 with the message the handler returned,
// and nothing is saved or flushed. Reset keeps its own check. FlushSection
// still checks for callers that are not this route.
func MayUpdateSettings() http.Middleware {
	return func(ctx http.Context) {
		if !policies.MayUpdateSettings(AccountRole(ctx)) {
			abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": settingssvc.ErrUpdateForbidden.Error()})
			return
		}
		ctx.Request().Next()
	}
}
