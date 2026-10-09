package withdrawals

import (
	"errors"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalrecords"
)

// mapError turns a service failure into the answer the dashboard has always
// given. A withdrawal refusal shares its mapping with the external surface,
// which must answer the same bytes. Anything unrecognised is a 500 that names
// the action and keeps the cause in the log.
func mapError(ctx http.Context, err error, action string) http.Response {
	switch {
	case errors.Is(err, withdrawalrecords.ErrNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "withdrawal not found")
	case errors.Is(err, withdrawalrecords.ErrNotPending):
		return responses.Fail(ctx, http.StatusUnprocessableEntity, responses.CodeUnprocessable, "only pending withdrawals can be cancelled")
	case errors.Is(err, withdraw.ErrFeeEstimateUnavailable):
		return responses.Fail(ctx, http.StatusUnprocessableEntity, "FEE_ESTIMATE_FAILED", "fee estimation unavailable")
	}
	if response := controllers.MapWithdrawalError(ctx, err); response != nil {
		return response
	}
	slog.Error("dashboard withdrawals", "action", action, "error", err)
	return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to "+action)
}
