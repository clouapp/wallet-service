package wallets

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// TransactionsController serves the dashboard wallet transaction routes.
type TransactionsController struct {
	transactions *walletrecords.Transactions
}

func NewTransactionsController(
	transactions *walletrecords.Transactions,
) *TransactionsController {
	if transactions == nil {
		panic("dashboard wallet transactions controller: transactions service is required")
	}
	return &TransactionsController{
		transactions: transactions,
	}
}

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
func (ctrl *TransactionsController) ListWalletTransactions(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)

	var query requests.ListWalletTransactionsRequest
	query.Load(ctx)
	limit, offset := pagination.ParseParams(ctx, 50)
	txType := query.Type
	status := query.Status
	transactions, total, err := ctrl.transactions.FindByWallet(ctx.Context(), wallet.ID, txType, status, limit, offset)
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
func (ctrl *TransactionsController) GetWalletTransaction(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)

	var path requests.WalletTransactionPathRequest
	path.Load(ctx)
	txIDStr := path.TxID
	tx, err := ctrl.transactions.FindByIDAndWallet(ctx.Context(), txIDStr, wallet.ID)
	if err != nil || tx == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "transaction not found"})
	}

	views := controllers.WalletTransactionViewsForChain(ctx.Context(), wallet.Chain, []models.Transaction{*tx})
	return ctx.Response().Json(http.StatusOK, views[0])
}
