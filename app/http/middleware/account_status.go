package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
)

// abortUnlessAccountAllows refuses a mutation on a frozen or archived
// account and reports whether the request may continue.
func abortUnlessAccountAllows(ctx http.Context, account *models.Account) bool {
	if account == nil {
		_ = responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "account not found").Abort()
		return false
	}
	if policies.AccountAllowsRequest(account.Status, ctx.Request().Method()) {
		return true
	}
	_ = responses.FailWith(ctx, http.StatusForbidden, responses.CodeAccountFrozen,
		"account is "+account.Status+"; only reads are allowed",
		map[string]any{"status": account.Status}).Abort()
	return false
}
