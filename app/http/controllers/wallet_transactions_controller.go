package controllers

import (
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/models"
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
	transactions, total, err := container.Get().TransactionRepo.FindByWallet(wallet.ID, txType, status, limit, offset)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch transactions"})
	}

	views := walletTransactionViews(transactions, loadAssetDecimalsCatalog(wallet.Chain))
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
	tx, err := container.Get().TransactionRepo.FindByIDAndWallet(txIDStr, wallet.ID)
	if err != nil || tx == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "transaction not found"})
	}

	views := walletTransactionViews([]models.Transaction{*tx}, loadAssetDecimalsCatalog(wallet.Chain))
	return ctx.Response().Json(http.StatusOK, views[0])
}

// loadAssetDecimalsCatalog reads the chain and its active tokens; a failed read
// leaves those decimals unknown instead of failing the listing.
func loadAssetDecimalsCatalog(chainID string) assetDecimalsCatalog {
	chainRecord, chainErr := container.Get().ChainRepo.FindByID(chainID)
	if chainErr != nil {
		slog.Warn("load chain for transaction decimals", "chain", chainID, "error", chainErr)
		chainRecord = nil
	}
	tokens, tokenErr := container.Get().TokenRepo.FindByChainID(chainID)
	if tokenErr != nil {
		slog.Warn("load tokens for transaction decimals", "chain", chainID, "error", tokenErr)
		tokens = nil
	}
	return newAssetDecimalsCatalog(chainRecord, tokens)
}
