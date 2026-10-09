package accounts

import (
	"errors"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/responses"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// mapError turns a platform account service failure into the answer the
// platform API has always given. Each refusal sentence is the service's own.
// Anything unrecognised is the generic 500; action labels it in the log.
func mapError(ctx http.Context, err error, action string) http.Response {
	switch {
	case errors.Is(err, accountsvc.ErrPlatformViewForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, accountsvc.ErrPlatformViewForbidden.Error())
	case errors.Is(err, accountsvc.ErrPlatformLifecycleForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, accountsvc.ErrPlatformLifecycleForbidden.Error())
	case errors.Is(err, accountsvc.ErrPlatformOwnersForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, accountsvc.ErrPlatformOwnersForbidden.Error())
	case errors.Is(err, accountsvc.ErrPlatformAccountUsersForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, accountsvc.ErrPlatformAccountUsersForbidden.Error())
	case errors.Is(err, accountsvc.ErrAccountNotFound):
		return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, accountsvc.ErrAccountNotFound.Error())
	case errors.Is(err, accountsvc.ErrPlatformOwnerUserNotFound):
		return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, accountsvc.ErrPlatformOwnerUserNotFound.Error())
	default:
		return controllers.MapInternalError(ctx, err, action)
	}
}
