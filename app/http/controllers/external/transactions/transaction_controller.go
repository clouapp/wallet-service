package transactions

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	resources "github.com/macrowallets/waas/app/http/resources/external/transactions"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/withdraw"
)

// TransactionController serves the external transaction routes.
type TransactionController struct {
	withdrawals *withdraw.Service
}

// NewTransactionController wires the external transaction handlers.
func NewTransactionController(withdrawals *withdraw.Service) *TransactionController {
	if withdrawals == nil {
		panic("external transactions controller: withdrawal service is required")
	}
	return &TransactionController{withdrawals: withdrawals}
}

// Index godoc
//
//	@Summary		List transactions
//	@Description	Returns a paginated list of transactions with optional filters by chain, type, status, or user. Always scoped to the authenticated account.
//	@Tags			Transactions
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		SignatureAuth
//	@Param			chain	query		string	false	"Chain ID filter"		example(eth)
//	@Param			type	query		string	false	"Transaction type"		Enums(deposit, withdrawal)
//	@Param			status	query		string	false	"Transaction status"	Enums(pending, confirmed, failed)
//	@Param			user_id	query		string	false	"External user ID filter"
//	@Param			limit	query		int		false	"Max results (default 50)"	example(50)
//	@Param			offset	query		int		false	"Pagination offset"			example(0)
//	@Success		200		{object}	resources.List
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		500		{object}	responses.ErrorBody
//	@Failure		429		{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/v1/transactions [get]
func (c *TransactionController) Index(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "unauthorized")
	}

	limit, offset := pagination.ParseParams(ctx, 50)

	rows, total, err := c.withdrawals.ListTransactionsForAccount(
		ctx.Context(),
		accountID,
		ctx.Request().Query("chain"),
		ctx.Request().Query("type"),
		ctx.Request().Query("status"),
		ctx.Request().Query("user_id"),
		limit,
		offset,
	)
	if err != nil {
		return mapError(ctx, err, "list_transactions")
	}

	return ctx.Response().Success().Json(pagination.Response(resources.TransactionsFrom(rows), total, limit, offset))
}

// Show godoc
//
//	@Summary		Get a transaction
//	@Description	Returns a single transaction by its UUID
//	@Tags			Transactions
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		SignatureAuth
//	@Param			id	path		string	true	"Transaction UUID"	format(uuid)
//	@Success		200	{object}	resources.Transaction
//	@Failure		400	{object}	responses.ErrorBody	"Invalid UUID"
//	@Failure		401	{object}	responses.ErrorBody
//	@Failure		404	{object}	responses.ErrorBody	"Transaction not found"
//	@Failure		500	{object}	responses.ErrorBody
//	@Failure		429	{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/v1/transactions/{id} [get]
func (c *TransactionController) Show(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "unauthorized")
	}

	id, err := requests.RouteUUID(ctx, "id")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid tx id")
	}

	tx, err := c.withdrawals.GetTransactionForAccount(ctx.Context(), accountID, id)
	if err != nil {
		return mapError(ctx, err, actionShow)
	}

	return ctx.Response().Success().Json(resources.TransactionFrom(*tx))
}

// IndexByUser godoc
//
//	@Summary		List transactions for a user
//	@Description	Returns paginated transactions for a specific external user ID across all chains
//	@Tags			Transactions
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		SignatureAuth
//	@Param			external_id	path		string	true	"External user identifier"	example(user_123)
//	@Param			limit		query		int		false	"Max results (default 50)"	example(50)
//	@Param			offset		query		int		false	"Pagination offset"			example(0)
//	@Success		200			{object}	resources.List
//	@Failure		500			{object}	responses.ErrorBody
//	@Failure		429			{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/v1/users/{external_id}/transactions [get]
func (c *TransactionController) IndexByUser(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "unauthorized")
	}

	limit, offset := pagination.ParseParams(ctx, 50)

	rows, total, err := c.withdrawals.ListTransactionsForAccount(
		ctx.Context(),
		accountID,
		"", "", "",
		ctx.Request().Route("external_id"),
		limit,
		offset,
	)
	if err != nil {
		return mapError(ctx, err, "list_user_transactions")
	}

	// Empty result when external_id belongs to another account — same body
	// as the legitimate "no transactions yet" case (IDOR mitigation).
	return ctx.Response().Success().Json(pagination.Response(resources.TransactionsFrom(rows), total, limit, offset))
}
