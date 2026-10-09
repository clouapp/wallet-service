package wallets

import (
	"context"
	"errors"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	walletresources "github.com/macrowallets/waas/app/http/resources/dashboard/wallets"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// TransactionsController serves the dashboard wallet transaction routes.
type TransactionsController struct {
	transactions *walletrecords.Transactions
	chains       *chainsvc.Service
}

func NewTransactionsController(
	transactions *walletrecords.Transactions,
	chains *chainsvc.Service,
) *TransactionsController {
	if transactions == nil {
		panic("dashboard wallet transactions controller: transactions service is required")
	}
	if chains == nil {
		panic("dashboard wallet transactions controller: chains service is required")
	}
	return &TransactionsController{
		transactions: transactions,
		chains:       chains,
	}
}

// assetCatalog reads the chain and its active tokens for the transaction
// decimals; a failed read leaves those decimals unknown instead of failing the
// response.
func (ctrl *TransactionsController) assetCatalog(ctx context.Context, chainID string) (*models.Chain, []models.Token) {
	chainRecord, chainErr := ctrl.chains.FindByID(ctx, chainID)
	if errors.Is(chainErr, models.ErrRepositoryNotFound) {
		chainRecord, chainErr = nil, nil
	}
	if chainErr != nil {
		slog.Warn("load chain for transaction decimals", "chain", chainID, "error", chainErr)
		chainRecord = nil
	}
	tokens, tokenErr := ctrl.chains.FindTokens(ctx, chainID)
	if tokenErr != nil {
		slog.Warn("load tokens for transaction decimals", "chain", chainID, "error", tokenErr)
		tokens = nil
	}
	return chainRecord, tokens
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
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /wallets/{walletId}/transactions [get]
func (ctrl *TransactionsController) ListWalletTransactions(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	limit, offset := pagination.ParseParams(ctx, 50)
	txType := ctx.Request().Query("type")
	status := ctx.Request().Query("status")
	transactions, total, err := ctrl.transactions.FindByWallet(ctx.Context(), wallet.ID, txType, status, limit, offset)
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to fetch transactions")
	}

	chainRecord, tokens := ctrl.assetCatalog(ctx.Context(), wallet.Chain)
	views := walletresources.TransactionsForChain(chainRecord, tokens, transactions)
	return ctx.Response().Success().Json(pagination.Response(views, total, limit, offset))
}

// GetWalletTransaction godoc
// @Summary      Get a single wallet transaction
// @Description  Returns a specific transaction by ID scoped to the wallet
// @Tags         Wallet Transactions
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path  string  true  "Wallet UUID"
// @Param        txId      path  string  true  "Transaction UUID"
// @Success      200  {object}  walletresources.Transaction
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /wallets/{walletId}/transactions/{txId} [get]
func (ctrl *TransactionsController) GetWalletTransaction(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	txIDStr := ctx.Request().Route("txId")
	tx, err := ctrl.transactions.FindByIDAndWallet(ctx.Context(), txIDStr, wallet.ID)
	if err != nil || tx == nil {
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "transaction not found")
	}

	chainRecord, tokens := ctrl.assetCatalog(ctx.Context(), wallet.Chain)
	views := walletresources.TransactionsForChain(chainRecord, tokens, []models.Transaction{*tx})
	return ctx.Response().Success().Json(views[0])
}

// WalletTransactionListResponse documents the paginated wallet transaction list.
type WalletTransactionListResponse struct {
	Data []walletresources.Transaction `json:"data"`
}
