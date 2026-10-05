package middleware

import (
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/policies"
	activitysvc "github.com/macrowallets/waas/app/services/activity"
)

// MayReadActivity refuses GET /v1/accounts/{accountId}/activity unless
// policies.MayReadActivity allows the account role AccountContext already
// stored. That permission is activity.read. Owner, admin, and auditor hold
// it. User does not, and the retired viewer label stays refused. A denial
// is 403 with the message the handler returned, and the activity list is
// not written. GET /v1/accounts/{accountId}/activity/{id} resolves the row
// in the handler first: a missing id is 404, and activity.read is asked
// only after that row is found.
func MayReadActivity() http.Middleware {
	return func(ctx http.Context) {
		if strings.TrimSpace(ctx.Request().Route("id")) != "" {
			ctx.Request().Next()
			return
		}
		if !policies.MayReadActivity(AccountRole(ctx)) {
			_ = responses.Send(ctx, http.StatusForbidden, http.Json{"error": activitysvc.ErrReadForbidden.Error()}).Abort()
			return
		}
		ctx.Request().Next()
	}
}
