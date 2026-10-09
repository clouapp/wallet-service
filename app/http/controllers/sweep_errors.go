package controllers

import (
	"errors"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/sweep"
)

// mapSweepFailure answers a failure of a sweep route: the sweep sentinels
// MapSweepError knows, an amount that is not a whole number of base units, and
// otherwise the generic 500 labelled with endpoint in the log.
func mapSweepFailure(ctx http.Context, err error, endpoint string) http.Response {
	if response := MapSweepError(ctx, err); response != nil {
		return response
	}
	if errors.Is(err, sweep.ErrInvalidAmount) {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, sweep.ErrInvalidAmount.Error())
	}
	return MapInternalError(ctx, err, endpoint)
}
