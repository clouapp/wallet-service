package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/policies"
	featuressvc "github.com/macrowallets/waas/app/services/features"
)

// MayViewAccountFeatures refuses GET /v1/accounts/{accountId}/features unless
// policies.MayViewSettings allows the account role AccountContext already
// stored. That permission is settings.read. Owner, admin, and auditor hold
// it. User does not, and the retired viewer label stays refused. A denial
// is 403 with the message the service returns, and the feature list is not
// written. List keeps its own check.
func MayViewAccountFeatures() http.Middleware {
	return func(ctx http.Context) {
		if !policies.MayViewSettings(AccountRole(ctx)) {
			abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": featuressvc.ErrViewForbidden.Error()})
			return
		}
		ctx.Request().Next()
	}
}
