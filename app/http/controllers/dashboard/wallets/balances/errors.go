package balances

import (
	"errors"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/walletview"
)

// mapError answers a failed balance read: 500 "failed to fetch <what>", naming
// the data that could not be read, with the cause logged and kept out of the body.
func mapError(ctx http.Context, err error, action string) http.Response {
	message := "failed to " + action
	var fetch *walletview.FetchError
	if errors.As(err, &fetch) {
		message = "failed to fetch " + fetch.What
	}
	slog.Error("wallet balances: "+action, "error", err)
	return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, message)
}
