package controllers

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/pagination"
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

	limit, offset := pagination.ParseParams(ctx, 50)
	txType := ctx.Request().Query("type", "")
	status := ctx.Request().Query("status", "")
	transactions, total, err := container.Get().TransactionRepo.FindByWallet(wallet.ID, txType, status, limit, offset)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch transactions"})
	}

	return ctx.Response().Json(http.StatusOK, pagination.Response(transactions, total, limit, offset))
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
	tx, err := container.Get().TransactionRepo.FindByIDAndWallet(txIDStr, wallet.ID)
	if err != nil || tx == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "transaction not found"})
	}

	return ctx.Response().Json(http.StatusOK, tx)
}
