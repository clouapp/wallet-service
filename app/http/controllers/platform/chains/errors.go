package chains

import (
	"errors"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	chainsrequests "github.com/macrowallets/waas/app/http/requests/platform/chains"
	"github.com/macrowallets/waas/app/http/responses"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

// mapError turns a body or chain service failure into the answer the
// platform API has always given. A body over the limit is 413, any other
// unreadable body is 400. Anything unrecognised is the generic 500; action
// labels it in the log.
func mapError(ctx http.Context, err error, action string) http.Response {
	var invalid *chainsvc.ValidationError
	switch {
	case errors.Is(err, chainsrequests.ErrBodyTooLarge):
		return responses.Fail(ctx, http.StatusRequestEntityTooLarge, responses.CodeRequestTooLarge, "request body is too large")
	case errors.Is(err, chainsrequests.ErrBodyInvalid):
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid request body")
	case errors.As(err, &invalid):
		return responses.FieldsFailed(ctx, invalid.Fields)
	case errors.Is(err, chainsvc.ErrNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "chain not found")
	case errors.Is(err, chainsvc.ErrPlatformForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, chainsvc.ErrPlatformForbidden.Error())
	default:
		return controllers.MapInternalError(ctx, err, action)
	}
}
