package withdrawals

import (
	"errors"
	"net/http"

	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/withdrawalrecords"
)

// mapError turns a service failure into the answer the external API has
// always given. A withdrawal refusal shares its mapping with the dashboard,
// which must answer the same bytes. Anything unrecognised is the generic 500;
// action labels it in the log.
func mapError(ctx contractshttp.Context, err error, action string) contractshttp.Response {
	if errors.Is(err, withdrawalrecords.ErrNotFound) {
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "withdrawal not found")
	}
	if response := controllers.MapWithdrawalError(ctx, err); response != nil {
		return response
	}
	return controllers.MapInternalError(ctx, err, action)
}
