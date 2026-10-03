package controllers

import (
	"errors"
	"io"

	contractsaccess "github.com/goravel/framework/contracts/auth/access"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
)

func validateRequest(ctx http.Context, req http.FormRequest) http.Response {
	return ValidateRequest(ctx, req)
}

// ValidateRequest is the form-request check shared with surface packages.
// The body stays here so dashboard and external handlers keep the same 422 bytes.
func ValidateRequest(ctx http.Context, req http.FormRequest) http.Response {
	if len(req.Rules(ctx)) == 0 {
		return bindRulelessRequest(ctx, req)
	}

	validationErrors, err := ctx.Request().ValidateRequest(req)
	if err != nil {
		if validationErrors != nil {
			return responses.ValidationFailed(ctx, validationErrors)
		}
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
	if validationErrors != nil {
		return responses.ValidationFailed(ctx, validationErrors)
	}
	return nil
}

// bindRulelessRequest fills a form request that declares no rules.
// ValidateRequest refuses an empty rule map before it binds, so a PATCH
// whose request has no rules used to answer 200 and ignore the body.
func bindRulelessRequest(ctx http.Context, req http.FormRequest) http.Response {
	request := ctx.Request().Origin()
	if request == nil || request.Body == nil || request.ContentLength == 0 {
		return nil
	}
	if err := ctx.Request().Bind(req); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
	return nil
}

// Deny maps a policy denial to HTTP 403. The body is the policy message, the
// same bytes the gate helper used to write. An allow returns nil.
func Deny(ctx http.Context, decision contractsaccess.Response) http.Response {
	if decision.Allowed() {
		return nil
	}
	return responses.Send(ctx, http.StatusForbidden, http.Json{"error": decision.Message()})
}
