package wallets

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
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
// @Success      200  {object}  WalletTransactionListResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /wallets/{walletId}/transactions [get]
func ListWalletTransactions(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)

	limit, offset := pagination.ParseParams(ctx, 50)
	txType := ctx.Request().Query("type", "")
	status := ctx.Request().Query("status", "")
	transactions, total, err := container.MustMake[*repositories.TransactionRepository]().FindByWallet(ctx.Context(), wallet.ID, txType, status, limit, offset)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch transactions"})
	}

	views := controllers.WalletTransactionViewsForChain(ctx.Context(), wallet.Chain, transactions)
	return ctx.Response().Json(http.StatusOK, pagination.Response(views, total, limit, offset))
}

// GetWalletTransaction godoc
// @Summary      Get a single wallet transaction
// @Description  Returns a specific transaction by ID scoped to the wallet
// @Tags         Wallet Transactions
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path  string  true  "Wallet UUID"
// @Param        txId      path  string  true  "Transaction UUID"
// @Success      200  {object}  WalletTransactionView
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /wallets/{walletId}/transactions/{txId} [get]
func GetWalletTransaction(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)

	txIDStr := ctx.Request().Route("txId")
	tx, err := container.MustMake[*repositories.TransactionRepository]().FindByIDAndWallet(ctx.Context(), txIDStr, wallet.ID)
	if err != nil || tx == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "transaction not found"})
	}

	views := controllers.WalletTransactionViewsForChain(ctx.Context(), wallet.Chain, []models.Transaction{*tx})
	return ctx.Response().Json(http.StatusOK, views[0])
}
