package transactions

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	walletresources "github.com/macrowallets/waas/app/http/resources/dashboard/wallets"
	"github.com/macrowallets/waas/app/services/walletview"
)

// TransactionController serves the dashboard wallet transaction routes.
type TransactionController struct {
	view *walletview.Service
}

// NewTransactionController wires the controller with the wallet reads.
func NewTransactionController(view *walletview.Service) *TransactionController {
	if view == nil {
		panic("dashboard wallet transactions controller: wallet view is required")
	}
	return &TransactionController{view: view}
}

// Index godoc
//
//	@Summary		List transactions for a wallet
//	@Description	Returns a paginated list of transactions for a specific wallet
//	@Tags			Wallet Transactions
//	@Security		BearerAuth
//	@Produce		json
//	@Param			walletId	path		string	true	"Wallet UUID"
//	@Param			type		query		string	false	"Transaction type filter"	Enums(deposit, withdrawal)
//	@Param			status		query		string	false	"Status filter"				Enums(pending, confirmed, failed)
//	@Param			limit		query		int		false	"Max results (default 50)"	example(50)
//	@Param			offset		query		int		false	"Pagination offset"			example(0)
//	@Success		200			{object}	WalletTransactionListResponse
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/wallets/{walletId}/transactions [get]
func (c *TransactionController) Index(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)
	limit, offset := pagination.ParseParams(ctx, 50)

	page, err := c.view.TransactionsOf(ctx.Context(), walletview.TransactionsInput{
		Wallet: wallet,
		Type:   ctx.Request().Query("type"),
		Status: ctx.Request().Query("status"),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return mapError(ctx, err, "fetch transactions")
	}

	views := walletresources.TransactionsForChain(page.Chain, page.Tokens, page.Transactions)
	return ctx.Response().Success().Json(pagination.Response(views, page.Total, limit, offset))
}

// Show godoc
//
//	@Summary		Get a single wallet transaction
//	@Description	Returns a specific transaction by ID scoped to the wallet
//	@Tags			Wallet Transactions
//	@Security		BearerAuth
//	@Produce		json
//	@Param			walletId	path		string	true	"Wallet UUID"
//	@Param			txId		path		string	true	"Transaction UUID"
//	@Success		200			{object}	walletresources.Transaction
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/wallets/{walletId}/transactions/{txId} [get]
func (c *TransactionController) Show(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	page, err := c.view.Transaction(ctx.Context(), wallet, ctx.Request().Route("txId"))
	if err != nil {
		return mapError(ctx, err, "fetch transaction")
	}

	views := walletresources.TransactionsForChain(page.Chain, page.Tokens, page.Transactions)
	return ctx.Response().Success().Json(views[0])
}

// WalletTransactionListResponse documents the paginated wallet transaction list.
type WalletTransactionListResponse struct {
	Data []walletresources.Transaction `json:"data"`
}
