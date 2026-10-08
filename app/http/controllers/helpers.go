package controllers

import (
	contractsaccess "github.com/goravel/framework/contracts/auth/access"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
)

// Deny maps a policy denial to HTTP 403. The body is the policy message, the
// same bytes the gate helper used to write. An allow returns nil.
func Deny(ctx http.Context, decision contractsaccess.Response) http.Response {
	if decision.Allowed() {
		return nil
	}
	return responses.FailMessage(ctx, http.StatusForbidden, decision.Message())
}
