package controllers

import (
	"errors"
	"io"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"
)

func validateRequest(ctx http.Context, req http.FormRequest) http.Response {
	if len(req.Rules(ctx)) == 0 {
		return bindRulelessRequest(ctx, req)
	}

	validationErrors, err := ctx.Request().ValidateRequest(req)
	if err != nil {
		if validationErrors != nil {
			return ctx.Response().Json(http.StatusUnprocessableEntity, validationErrors.All())
		}
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
	if validationErrors != nil {
		return ctx.Response().Json(http.StatusUnprocessableEntity, validationErrors.All())
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
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
	return nil
}

func authorize(ctx http.Context, ability string, arguments map[string]any) http.Response {
	response := facades.Gate().WithContext(ctx).Inspect(ability, arguments)
	if response.Allowed() {
		return nil
	}
	return ctx.Response().Json(http.StatusForbidden, http.Json{"error": response.Message()})
}
