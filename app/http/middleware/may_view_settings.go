package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/policies"
	settingssvc "github.com/macrowallets/waas/app/services/settings"
)

// MayViewSettings refuses GET /v1/accounts/{accountId}/settings unless
// policies.MayViewSettings allows the account role AccountContext already
// stored. That permission is settings.read. Owner, admin, and auditor hold
// it. User does not, and the retired viewer label stays refused. A denial
// is 403 with the message the handler returned, and the settings body is
// not written. ShowGroup, update, flush, and reset keep their own checks.
func MayViewSettings() http.Middleware {
	return func(ctx http.Context) {
		if !policies.MayViewSettings(AccountRole(ctx)) {
			abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": settingssvc.ErrViewForbidden.Error()})
			return
		}
		ctx.Request().Next()
	}
}
