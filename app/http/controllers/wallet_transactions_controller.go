package controllers

import (
	"strconv"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macromarkets/vault/app/models"
)

// ListWalletTransactions godoc
// @Summary      List transactions for a wallet
// @Description  Returns a paginated list of transactions for a specific wallet
// @Tags         Wallet Transactions
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path    string  true   "Wallet UUID"
// @Param        type      query   string  false  "Transaction type filter"   Enums(deposit, withdrawal)
// @Param        status    query   string  false  "Status filter"             Enums(pending, confirmed, failed)
// @Param        limit     query   int     false  "Max results (default 50)"  example(50)
// @Param        offset    query   int     false  "Pagination offset"         example(0)
// @Success      200  {object}  TransactionListResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /wallets/{walletId}/transactions [get]
func ListWalletTransactions(ctx http.Context) http.Response {
	wallet, _, _, errResp := walletFromParam(ctx)
	if errResp != nil {
		return errResp
	}

	limit, _ := strconv.Atoi(ctx.Request().Query("limit", "50"))
	offset, _ := strconv.Atoi(ctx.Request().Query("offset", "0"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	query := facades.Orm().Query().
		Where("wallet_id = ?", wallet.ID).
		Limit(limit).
		Offset(offset)

	if txType := ctx.Request().Query("type", ""); txType != "" {
		query = query.Where("tx_type = ?", txType)
	}
	if status := ctx.Request().Query("status", ""); status != "" {
		query = query.Where("status = ?", status)
	}

	var transactions []models.Transaction
	if err := query.Find(&transactions); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch transactions"})
	}

	return ctx.Response().Json(http.StatusOK, http.Json{"data": transactions})
}

// GetWalletTransaction godoc
// @Summary      Get a single wallet transaction
// @Description  Returns a specific transaction by ID scoped to the wallet
// @Tags         Wallet Transactions
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path  string  true  "Wallet UUID"
// @Param        txId      path  string  true  "Transaction UUID"
// @Success      200  {object}  models.Transaction
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /wallets/{walletId}/transactions/{txId} [get]
func GetWalletTransaction(ctx http.Context) http.Response {
	wallet, _, _, errResp := walletFromParam(ctx)
	if errResp != nil {
		return errResp
	}

	txIDStr := ctx.Request().Route("txId")
	var tx models.Transaction
	if err := facades.Orm().Query().
		Where("id = ? AND wallet_id = ?", txIDStr, wallet.ID).
		First(&tx); err != nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "transaction not found"})
	}

	return ctx.Response().Json(http.StatusOK, &tx)
}
