package features

import (
	"errors"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	featuressvc "github.com/macrowallets/waas/app/services/features"
)

// mapError answers an error of the features service. A sentinel keeps the
// status and the words the route has always answered; anything else is logged
// and answered 500 with failure.
func mapError(ctx http.Context, err error, failure string) http.Response {
	switch {
	case errors.Is(err, featuressvc.ErrNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "feature not found")
	case errors.Is(err, featuressvc.ErrViewForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, featuressvc.ErrViewForbidden.Error())
	case errors.Is(err, featuressvc.ErrUpdateForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, featuressvc.ErrUpdateForbidden.Error())
	default:
		slog.Error("controller internal error", "endpoint", failure, "error", err)
		return responses.FailMessage(ctx, http.StatusInternalServerError, failure)
	}
}
