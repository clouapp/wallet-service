package controllers

import (
	"strconv"

	"github.com/goravel/framework/contracts/http"
	"github.com/google/uuid"

	"github.com/macromarkets/vault/app/container"
)

// ListTransactions godoc
// @Summary      List transactions
// @Description  Returns a paginated list of transactions with optional filters by chain, type, status, or user
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
// @Failure      500      {object}  ErrorResponse
// @Router       /v1/transactions [get]
func ListTransactions(ctx http.Context) http.Response {
	limit, _ := strconv.Atoi(ctx.Request().Query("limit", "50"))
	offset, _ := strconv.Atoi(ctx.Request().Query("offset", "0"))

	txs, err := container.Get().WithdrawalService.ListTransactions(
		ctx.Context(),
		ctx.Request().Query("chain", ""),
		ctx.Request().Query("type", ""),
		ctx.Request().Query("status", ""),
		ctx.Request().Query("user_id", ""),
		limit,
		offset,
	)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{
			"error": err.Error(),
		})
	}
	return ctx.Response().Success().Json(http.Json{
		"data": txs,
	})
}

// GetTransaction godoc
// @Summary      Get a transaction
// @Description  Returns a single transaction by its UUID
// @Tags         Transactions
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        id   path      string  true  "Transaction UUID"  format(uuid)
// @Success      200  {object}  models.Transaction
// @Failure      400  {object}  ErrorResponse  "Invalid UUID"
// @Failure      404  {object}  ErrorResponse  "Transaction not found"
// @Router       /v1/transactions/{id} [get]
func GetTransaction(ctx http.Context) http.Response {
	id, err := uuid.Parse(ctx.Request().Route("id"))
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{
			"error": "invalid tx id",
		})
	}
	tx, err := container.Get().WithdrawalService.GetTransaction(ctx.Context(), id)
	if err != nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{
			"error": "transaction not found",
		})
	}
	return ctx.Response().Success().Json(tx)
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
func ListUserTransactions(ctx http.Context) http.Response {
	limit, _ := strconv.Atoi(ctx.Request().Query("limit", "50"))
	offset, _ := strconv.Atoi(ctx.Request().Query("offset", "0"))

	txs, err := container.Get().WithdrawalService.ListTransactions(
		ctx.Context(),
		"", "", "",
		ctx.Request().Route("external_id"),
		limit,
		offset,
	)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{
			"error": err.Error(),
		})
	}
	return ctx.Response().Success().Json(http.Json{
		"data": txs,
	})
}
