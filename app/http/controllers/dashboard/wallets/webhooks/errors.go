package webhooks

import (
	"errors"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// mapError answers a wallet webhook failure. A webhook the wallet does not hold
// is 404 and a test delivery the URL refused 502, without the cause. Any other
// failure is 500 "failed to <action>", with the cause logged and kept out of the
// body.
func mapError(ctx http.Context, err error, action string) http.Response {
	switch {
	case errors.Is(err, walletrecords.ErrWebhookNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, walletrecords.ErrWebhookNotFound.Error())
	case errors.Is(err, walletrecords.ErrWebhookTestFailed):
		slog.Info("wallet webhooks: "+action, "error", err)
		return responses.Fail(ctx, http.StatusBadGateway, responses.CodeProviderUnavailable, "webhook test delivery failed")
	}
	slog.Error("wallet webhooks: "+action, "error", err)
	return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to "+action)
}
