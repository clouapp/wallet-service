package users

import (
	"errors"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/responses"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

// mapError turns a platform user service failure into the answer the
// platform API has always given. Each refusal sentence is the service's own.
// Anything unrecognised is the generic 500; action labels it in the log.
func mapError(ctx http.Context, err error, action string) http.Response {
	switch {
	case errors.Is(err, usersvc.ErrViewForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, usersvc.ErrViewForbidden.Error())
	case errors.Is(err, usersvc.ErrPlatformForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, usersvc.ErrPlatformForbidden.Error())
	case errors.Is(err, usersvc.ErrSessionsForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, usersvc.ErrSessionsForbidden.Error())
	case errors.Is(err, usersvc.ErrMFAForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, usersvc.ErrMFAForbidden.Error())
	case errors.Is(err, usersvc.ErrNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "user not found")
	default:
		return controllers.MapInternalError(ctx, err, action)
	}
}
