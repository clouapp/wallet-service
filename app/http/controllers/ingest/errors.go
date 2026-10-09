package ingest

import (
	"errors"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	ingestsvc "github.com/macrowallets/waas/app/services/ingest"
)

// mapError turns an ingest failure into the answer providers have always
// got. Anything unrecognised is the generic 500, and the cause stays in the log.
func mapError(ctx http.Context, err error) http.Response {
	switch {
	case errors.Is(err, ingestsvc.ErrUnknownProvider):
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "unknown provider")
	case errors.Is(err, ingestsvc.ErrInvalidPayload):
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid payload")
	default:
		return responses.InternalError(ctx, err)
	}
}
