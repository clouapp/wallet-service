package transactions

import (
	"errors"
	"net/http"

	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/withdraw"
)

// actionShow labels the single-transaction read.
const actionShow = "get_transaction"

// mapError turns a transaction read failure into the answer the external API
// gives. A transaction that is missing, or is another account's, is a 404; any
// other failure is the generic 500 and action labels it in the log.
func mapError(ctx contractshttp.Context, err error, action string) contractshttp.Response {
	if errors.Is(err, withdraw.ErrTransactionNotFound) {
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "transaction not found")
	}
	return controllers.MapInternalError(ctx, err, action)
}
