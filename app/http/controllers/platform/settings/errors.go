package settings

import (
	"errors"

	"github.com/goravel/framework/contracts/http"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	settingssvc "github.com/macrowallets/waas/app/services/settings"
)

// mailTestFailedMessage is the 502 text. It names neither the SMTP password
// nor the host credentials. The provider error stays off the wire and the log.
const mailTestFailedMessage = "the test message was not sent"

// mapError turns a body or settings service failure into the answer the
// platform API has always given. A body its form request refused is that
// answer. A body over the limit is 413, any other unreadable body is 400. A failed mail test is a 502 that carries no provider
// text. Anything unrecognised is the generic 500; action labels it in the log.
func mapError(ctx http.Context, err error, action string) http.Response {
	var refusal *requests.Refusal
	var invalid *settingssvc.ValidationError
	switch {
	case errors.As(err, &refusal):
		return refusal.Response
	case errors.Is(err, requests.ErrAccountSettingsBodyTooLarge):
		return responses.Fail(ctx, http.StatusRequestEntityTooLarge, responses.CodeRequestTooLarge, "request body is too large")
	case errors.Is(err, requests.ErrAccountSettingsBodyInvalid):
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid request body")
	case errors.As(err, &invalid):
		return responses.FieldsFailed(ctx, invalid.Fields)
	case errors.Is(err, settingssvc.ErrGroupNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "settings group not found")
	case errors.Is(err, settingssvc.ErrAccountNotFound):
		return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, settingssvc.ErrAccountNotFound.Error())
	case errors.Is(err, settingssvc.ErrSectionNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "settings section not found")
	case errors.Is(err, settingssvc.ErrPlatformForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, settingssvc.ErrPlatformForbidden.Error())
	case errors.Is(err, settingssvc.ErrPlatformViewForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, settingssvc.ErrPlatformViewForbidden.Error())
	case errors.Is(err, settingssvc.ErrPlatformTestMail):
		appfacades.Log().Error(mailTestFailedMessage)
		return responses.Fail(ctx, http.StatusBadGateway, responses.CodeProviderUnavailable, mailTestFailedMessage)
	default:
		return controllers.MapInternalError(ctx, err, action)
	}
}
