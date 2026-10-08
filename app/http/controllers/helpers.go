package controllers

import (
	contractsaccess "github.com/goravel/framework/contracts/auth/access"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
)

func validateRequest(ctx http.Context, req http.FormRequest) http.Response {
	return ValidateRequest(ctx, req)
}

// ValidateRequest is the form-request check shared with surface packages.
// The body lives in requests.Validate so dashboard and external handlers keep the same 422 bytes.
func ValidateRequest(ctx http.Context, req http.FormRequest) http.Response {
	return requests.Validate(ctx, req)
}

// Deny maps a policy denial to HTTP 403. The body is the policy message, the
// same bytes the gate helper used to write. An allow returns nil.
func Deny(ctx http.Context, decision contractsaccess.Response) http.Response {
	if decision.Allowed() {
		return nil
	}
	return responses.FailMessage(ctx, http.StatusForbidden, decision.Message())
}
