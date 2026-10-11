package transactions

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	transactionsrequests "github.com/macrowallets/waas/app/http/requests/external/transactions"
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
//	@Description	Returns a page of the account's transactions, optionally filtered by chain, type, status or user. Always scoped to the authenticated account.
//	@Description
//	@Description	Without updated_since or cursor it is an offset page, newest first (created_at descending).
//	@Description
//	@Description	With updated_since or cursor it reads the changes feed: transactions in (updated_at, id) ascending order, from updated_since (inclusive) or strictly after the cursor, whichever comes later, with the same filters; limit defaults to 50 and is capped at 200, offset is refused unless 0, and the body adds next_cursor (null on the last page). Pass next_cursor as cursor, with the same filters, to read the next page. Every change of a transaction (status, confirmations, failure, fee, block) moves its updated_at, so a transaction comes back after each change: credit idempotently by transaction_id.
//	@Description
//	@Description	updated_at has one-second precision and is stamped when the write runs, not when it commits, so a change can become visible after a reader has paged past its second. A reconciler should therefore start each run from updated_since = start of its last complete run minus a few minutes, and follow next_cursor until it is null, rather than keep a cursor between runs.
//	@Tags			Transactions
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		SignatureAuth
//	@Param			chain			query		string	false	"Chain ID filter"		example(eth)
//	@Param			type			query		string	false	"Transaction type"		Enums(deposit, withdrawal)
//	@Param			status			query		string	false	"Transaction status"	Enums(pending, confirming, confirmed, failed)
//	@Param			user_id			query		string	false	"External user ID filter"
//	@Param			updated_since	query		string	false	"Changes feed: first updated_at to return, RFC 3339 (URL-encode a + offset)"	format(date-time)	example(2026-10-11T12:00:00Z)
//	@Param			cursor			query		string	false	"Changes feed: next_cursor of the previous page"
//	@Param			limit			query		int		false	"Max results (default 50; the changes feed caps it at 200)"	example(50)
//	@Param			offset			query		int		false	"Pagination offset (offset page only)"	example(0)
//	@Success		200				{object}	resources.List
//	@Failure		400				{object}	responses.ErrorBody	"Invalid updated_since, cursor, limit or offset on the changes feed (invalid_request)"
//	@Failure		401				{object}	responses.ErrorBody
//	@Failure		500				{object}	responses.ErrorBody
//	@Failure		429				{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/v1/transactions [get]
func (c *TransactionController) Index(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "unauthorized")
	}

	changes, isFeed, err := transactionsrequests.ParseChanges(ctx)
	if err != nil {
		return mapQueryError(ctx, err)
	}
	if isFeed {
		return c.changes(ctx, accountID, changes)
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

// changes answers one page of the changes feed Index serves.
func (c *TransactionController) changes(ctx http.Context, accountID uuid.UUID, query transactionsrequests.Changes) http.Response {
	page, err := c.withdrawals.ListTransactionChanges(ctx.Context(), withdraw.TransactionChangesInput{
		AccountID:      accountID,
		Chain:          query.Chain,
		TxType:         query.Type,
		Status:         query.Status,
		ExternalUserID: query.UserID,
		UpdatedSince:   query.UpdatedSince,
		After:          withdraw.TransactionPosition{UpdatedAt: query.AfterUpdatedAt, ID: query.AfterID},
		Limit:          query.Limit,
	})
	if err != nil {
		return mapError(ctx, err, "list_transaction_changes")
	}

	return ctx.Response().Success().Json(pagination.CursorResponse(
		resources.TransactionsFrom(page.Transactions), page.Total, query.Limit, resources.NextCursor(page.Next)))
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
