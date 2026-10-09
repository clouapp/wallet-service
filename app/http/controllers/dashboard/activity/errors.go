package activity

import (
	"errors"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	activitysvc "github.com/macrowallets/waas/app/services/activity"
)

// mapError answers an error of the activity service. A sentinel keeps the
// status and the words the route has always answered; anything else is logged
// and answered 500 with failure.
func mapError(ctx http.Context, err error, failure string) http.Response {
	switch {
	case errors.Is(err, activitysvc.ErrNotFound):
		return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, activitysvc.ErrNotFound.Error())
	case errors.Is(err, activitysvc.ErrReadForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, activitysvc.ErrReadForbidden.Error())
	case errors.Is(err, activitysvc.ErrPlatformForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, activitysvc.ErrPlatformForbidden.Error())
	default:
		slog.Error("controller internal error", "endpoint", failure, "error", err)
		return responses.FailMessage(ctx, http.StatusInternalServerError, failure)
	}
}
