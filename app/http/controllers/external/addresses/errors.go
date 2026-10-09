package addresses

import (
	"errors"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/walletops"
)

// mapError answers a failed address lookup. An address the account does not own
// is the same 404 as one that does not exist. Any other failure is the generic
// 500, with the cause logged and labelled by action.
func mapError(ctx http.Context, err error, action string) http.Response {
	if errors.Is(err, walletops.ErrAddressNotFound) {
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "address not found")
	}
	return controllers.MapInternalError(ctx, err, action)
}
