package transactions

import (
	"errors"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/walletview"
)

// mapError answers a failed transaction read. A transaction the wallet does not
// hold is 404. Any other failure is 500 "failed to fetch <what>", with the cause
// logged and kept out of the body.
func mapError(ctx http.Context, err error, action string) http.Response {
	if errors.Is(err, walletview.ErrTransactionNotFound) {
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, walletview.ErrTransactionNotFound.Error())
	}
	message := "failed to " + action
	var fetch *walletview.FetchError
	if errors.As(err, &fetch) {
		message = "failed to fetch " + fetch.What
	}
	slog.Error("wallet transactions: "+action, "error", err)
	return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, message)
}
