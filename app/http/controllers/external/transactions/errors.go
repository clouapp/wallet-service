package transactions

import (
	"net/http"

	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/responses"
)

// actionShow labels the single-transaction read.
const actionShow = "get_transaction"

// mapError turns a transaction read failure into the answer the external API
// has always given. A transaction that cannot be read is answered as missing,
// whatever the cause; a failed list is the generic 500 and action labels it in
// the log.
func mapError(ctx contractshttp.Context, err error, action string) contractshttp.Response {
	if action == actionShow {
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "transaction not found")
	}
	return controllers.MapInternalError(ctx, err, action)
}
