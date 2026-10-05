package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/policies"
	activitysvc "github.com/macrowallets/waas/app/services/activity"
)

// MayReadActivity refuses GET /v1/accounts/{accountId}/activity and
// GET /v1/accounts/{accountId}/activity/{id} unless policies.MayReadActivity
// allows the account role AccountContext already stored. That permission is
// activity.read. Owner, admin, and auditor hold it. User does not, and the
// retired viewer label stays refused. A denial is 403 with the message the
// handler returned, and neither the activity list nor the event is written.
// List and Get keep their own checks.
func MayReadActivity() http.Middleware {
	return func(ctx http.Context) {
		if !policies.MayReadActivity(AccountRole(ctx)) {
			_ = responses.Send(ctx, http.StatusForbidden, http.Json{"error": activitysvc.ErrReadForbidden.Error()}).Abort()
			return
		}
		ctx.Request().Next()
	}
}
