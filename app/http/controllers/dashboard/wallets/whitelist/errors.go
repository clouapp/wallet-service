package whitelist

import (
	"errors"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// mapError answers a whitelist failure. An entry the wallet does not hold is 404.
// Any other failure is 500 "failed to <action>", with the cause logged and kept
// out of the body.
func mapError(ctx http.Context, err error, action string) http.Response {
	if errors.Is(err, walletrecords.ErrWhitelistEntryNotFound) {
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, walletrecords.ErrWhitelistEntryNotFound.Error())
	}
	slog.Error("wallet whitelist: "+action, "error", err)
	return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to "+action)
}
