package controllers

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"
)

func validateRequest(ctx http.Context, req http.FormRequest) http.Response {
	if len(req.Rules(ctx)) == 0 {
		return nil
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

func authorize(ctx http.Context, ability string, arguments map[string]any) http.Response {
	response := facades.Gate().WithContext(ctx).Inspect(ability, arguments)
	if response.Allowed() {
		return nil
	}
	return ctx.Response().Json(http.StatusForbidden, http.Json{"error": response.Message()})
}
