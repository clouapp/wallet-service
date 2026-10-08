package transactions

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	exttransactions "github.com/macrowallets/waas/app/http/resources/external/transactions"
	"github.com/macrowallets/waas/app/http/responses"
	withdraw "github.com/macrowallets/waas/app/services/withdraw"
)

// TransactionsController serves the external transaction routes.
type TransactionsController struct {
	withdrawals *withdraw.Service
}

func NewTransactionsController(
	withdrawals *withdraw.Service,
) *TransactionsController {
	if withdrawals == nil {
		panic("external transactions controller: withdrawal service is required")
	}
	return &TransactionsController{
		withdrawals: withdrawals,
	}
}

// ListTransactions godoc
// @Summary      List transactions
// @Description  Returns a paginated list of transactions with optional filters by chain, type, status, or user. Always scoped to the authenticated account.
// @Tags         Transactions
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        chain    query     string  false  "Chain ID filter"        example(eth)
// @Param        type     query     string  false  "Transaction type"       Enums(deposit, withdrawal)
// @Param        status   query     string  false  "Transaction status"     Enums(pending, confirmed, failed)
// @Param        user_id  query     string  false  "External user ID filter"
// @Param        limit    query     int     false  "Max results (default 50)"  example(50)
// @Param        offset   query     int     false  "Pagination offset"         example(0)
// @Success      200      {object}  TransactionListResponse
// @Failure      401      {object}  ErrorResponse
// @Failure      500      {object}  ErrorResponse
// @Router       /v1/transactions [get]
func (ctrl *TransactionsController) ListTransactions(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "unauthorized")
	}

	limit, offset := pagination.ParseParams(ctx, 50)
	var query requests.ListTransactionsRequest
	query.Load(ctx)

	txs, total, err := ctrl.withdrawals.ListTransactionsForAccount(
		ctx.Context(),
		accountID,
		query.Chain,
		query.Type,
		query.Status,
		query.UserID,
		limit,
		offset,
	)
	if err != nil {
		return controllers.MapInternalError(ctx, err, "list_transactions")
	}
	return responses.Send(ctx, http.StatusOK, pagination.Response(exttransactions.TransactionsFrom(txs), total, limit, offset))
}

// GetTransaction godoc
// @Summary      Get a transaction
// @Description  Returns a single transaction by its UUID
// @Tags         Transactions
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        id   path      string  true  "Transaction UUID"  format(uuid)
// @Success      200  {object}  exttransactions.Transaction
// @Failure      400  {object}  ErrorResponse  "Invalid UUID"
// @Failure      404  {object}  ErrorResponse  "Transaction not found"
// @Router       /v1/transactions/{id} [get]
func (ctrl *TransactionsController) GetTransaction(ctx http.Context) http.Response {
	id, err := requests.RouteUUID(ctx, "id")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid tx id")
	}
	tx, err := ctrl.withdrawals.GetTransaction(ctx.Context(), id)
	if err != nil || tx == nil {
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "transaction not found")
	}
	return ctx.Response().Success().Json(exttransactions.TransactionFrom(*tx))
}

// ListUserTransactions godoc
// @Summary      List transactions for a user
// @Description  Returns paginated transactions for a specific external user ID across all chains
// @Tags         Transactions
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        external_id  path      string  true   "External user identifier"  example(user_123)
// @Param        limit        query     int     false  "Max results (default 50)"   example(50)
// @Param        offset       query     int     false  "Pagination offset"           example(0)
// @Success      200          {object}  TransactionListResponse
// @Failure      500          {object}  ErrorResponse
// @Router       /v1/users/{external_id}/transactions [get]
func (ctrl *TransactionsController) ListUserTransactions(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "unauthorized")
	}

	limit, offset := pagination.ParseParams(ctx, 50)

	var path requests.ExternalIDRequest
	path.Load(ctx)
	txs, total, err := ctrl.withdrawals.ListTransactionsForAccount(
		ctx.Context(),
		accountID,
		"", "", "",
		path.ExternalID,
		limit,
		offset,
	)
	if err != nil {
		return controllers.MapInternalError(ctx, err, "list_user_transactions")
	}
	// Empty result when external_id belongs to another account — same body
	// as the legitimate "no transactions yet" case (IDOR mitigation).
	return responses.Send(ctx, http.StatusOK, pagination.Response(exttransactions.TransactionsFrom(txs), total, limit, offset))
}

// TransactionListResponse documents the paginated account transaction list.
type TransactionListResponse struct {
	Data []exttransactions.Transaction `json:"data"`
}
