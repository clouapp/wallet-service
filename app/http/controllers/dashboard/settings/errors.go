package settings

import (
	"errors"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	settingssvc "github.com/macrowallets/waas/app/services/settings"
)

// mapError answers an error of the settings service on the account settings
// routes. A field refusal is the 422 envelope with the service's messages; a
// sentinel keeps the status and the words the route has always answered;
// anything else is logged and answered 500 with failure.
func mapError(ctx http.Context, err error, failure string) http.Response {
	var invalid *settingssvc.ValidationError
	if errors.As(err, &invalid) {
		return responses.FieldsFailed(ctx, invalid.Fields)
	}
	switch {
	case errors.Is(err, settingssvc.ErrGroupNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "settings group not found")
	case errors.Is(err, settingssvc.ErrSectionNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "settings section not found")
	case errors.Is(err, settingssvc.ErrViewForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, settingssvc.ErrViewForbidden.Error())
	case errors.Is(err, settingssvc.ErrUpdateForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, settingssvc.ErrUpdateForbidden.Error())
	case errors.Is(err, settingssvc.ErrManagedByPlatform):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, settingssvc.ErrManagedByPlatform.Error())
	default:
		slog.Error("controller internal error", "endpoint", failure, "error", err)
		return responses.FailMessage(ctx, http.StatusInternalServerError, failure)
	}
}

// mapDocumentError answers a settings body that could not be read: too large
// is 413, anything else 400.
func mapDocumentError(ctx http.Context, err error) http.Response {
	if errors.Is(err, requests.ErrAccountSettingsBodyTooLarge) {
		return responses.Fail(ctx, http.StatusRequestEntityTooLarge, responses.CodeRequestTooLarge, "request body is too large")
	}
	return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid request body")
}
