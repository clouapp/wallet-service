package controllers

import (
	"strconv"

	"github.com/goravel/framework/contracts/http"
	"github.com/google/uuid"

	"github.com/macromarkets/vault/app/container"
)

// ListTransactions returns paginated list of transactions with optional filters
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

// GetTransaction returns a single transaction by ID
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

// ListUserTransactions returns all transactions for a specific user
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
