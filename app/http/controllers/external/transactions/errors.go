package transactions

import (
	"errors"
	"net/http"

	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/pagination"
	transactionsrequests "github.com/macrowallets/waas/app/http/requests/external/transactions"
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

// queryRefusals are the changes feed query refusals, each answered 400 with its
// own sentence.
var queryRefusals = []error{
	transactionsrequests.ErrInvalidUpdatedSince,
	transactionsrequests.ErrOffsetWithCursor,
	pagination.ErrInvalidCursor,
	pagination.ErrInvalidLimit,
	pagination.ErrInvalidOffset,
}

// mapQueryError answers a changes feed query the list cannot read: a refusal
// is 400 invalid_request with the refusal's sentence; anything else is the
// generic 500.
func mapQueryError(ctx contractshttp.Context, err error) contractshttp.Response {
	for _, refusal := range queryRefusals {
		if errors.Is(err, refusal) {
			return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, refusal.Error())
		}
	}
	return controllers.MapInternalError(ctx, err, "parse_transaction_changes")
}
