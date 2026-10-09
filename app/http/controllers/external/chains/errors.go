package chains

import (
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
)

// mapError answers a chain service failure: 500 "failed to <action>", with the
// cause logged and kept out of the body.
func mapError(ctx http.Context, err error, action string) http.Response {
	slog.Error("chains: "+action, "error", err)
	return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to "+action)
}
