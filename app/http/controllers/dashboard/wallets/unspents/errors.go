package unspents

import (
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
)

// mapError answers a failed UTXO read with the generic internal error. The cause
// is logged under action and kept out of the body.
func mapError(ctx http.Context, err error, action string) http.Response {
	slog.Error("wallet unspents: "+action, "error", err)
	return responses.InternalError(ctx, nil)
}
