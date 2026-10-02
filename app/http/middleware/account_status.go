package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
)

// abortUnlessAccountAllows refuses a mutation on a frozen or archived
// account and reports whether the request may continue.
func abortUnlessAccountAllows(ctx http.Context, account *models.Account) bool {
	if account == nil {
		abortWithJSON(ctx, http.StatusNotFound, http.Json{"error": "account not found"})
		return false
	}
	if policies.AccountAllowsRequest(account.Status, ctx.Request().Method()) {
		return true
	}
	abortWithJSON(ctx, http.StatusForbidden, http.Json{
		"error":  "account is " + account.Status + "; only reads are allowed",
		"status": account.Status,
	})
	return false
}
