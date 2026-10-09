package features

import (
	"errors"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	featuresrequests "github.com/macrowallets/waas/app/http/requests/platform/features"
	"github.com/macrowallets/waas/app/http/responses"
	featuressvc "github.com/macrowallets/waas/app/services/features"
)

// mapError turns a body or feature service failure into the answer the
// platform API has always given. A body over the limit is 413, a body that
// omits what the route needs is a 422 on that field, any other unreadable body
// is 400. Anything unrecognised is the generic 500; action labels it in the log.
func mapError(ctx http.Context, err error, action string) http.Response {
	switch {
	case errors.Is(err, featuresrequests.ErrBodyTooLarge):
		return responses.Fail(ctx, http.StatusRequestEntityTooLarge, responses.CodeRequestTooLarge, "request body is too large")
	case errors.Is(err, featuresrequests.ErrEnabledRequired):
		return responses.FieldError(ctx, "enabled", "enabled is required")
	case errors.Is(err, featuresrequests.ErrFeaturesRequired):
		return responses.FieldsFailed(ctx, map[string][]string{
			"features": {"features is required"},
		})
	case errors.Is(err, featuresrequests.ErrDuplicateKey), errors.Is(err, featuressvc.ErrDuplicateWrite):
		return responses.FieldsFailed(ctx, map[string][]string{
			"features": {"feature key is duplicated"},
		})
	case errors.Is(err, featuresrequests.ErrBodyInvalid):
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid request body")
	case errors.Is(err, featuressvc.ErrNotFound):
		return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, featuressvc.ErrNotFound.Error())
	case errors.Is(err, featuressvc.ErrScopeNotFound):
		return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, featuressvc.ErrScopeNotFound.Error())
	case errors.Is(err, featuressvc.ErrAccountNotFound):
		return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, featuressvc.ErrAccountNotFound.Error())
	case errors.Is(err, featuressvc.ErrInvalidAccountID):
		return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, featuressvc.ErrInvalidAccountID.Error())
	case errors.Is(err, featuressvc.ErrPlatformForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, featuressvc.ErrPlatformForbidden.Error())
	default:
		return controllers.MapInternalError(ctx, err, action)
	}
}
