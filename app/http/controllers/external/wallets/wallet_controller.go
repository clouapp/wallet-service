package wallets

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	walletsrequests "github.com/macrowallets/waas/app/http/requests/wallets"
	walletresource "github.com/macrowallets/waas/app/http/resources/dashboard/wallets"
	walletsresources "github.com/macrowallets/waas/app/http/resources/wallets"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/walletops"
	"github.com/macrowallets/waas/app/services/walletview"
)

// WalletController serves the external wallet list, create, and get routes.
type WalletController struct {
	view *walletview.Service
	ops  *walletops.Service
}

// NewWalletController wires the controller with the wallet reads and the wallet
// operations.
func NewWalletController(view *walletview.Service, ops *walletops.Service) *WalletController {
	if view == nil {
		panic("external wallets controller: wallet view is required")
	}
	if ops == nil {
		panic("external wallets controller: wallet operations are required")
	}
	return &WalletController{view: view, ops: ops}
}

// Store godoc
//
//	@Summary		Create a new wallet
//	@Description	Creates a new HD wallet for the specified blockchain. Only one wallet per chain is allowed.
//	@Description	The response carries the wallet fields and `service_public_key`.
//	@Tags			Wallets
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		SignatureAuth
//	@Param			body	body		CreateWalletSwagger	true	"Wallet creation request"
//	@Success		201		{object}	walletsresources.Created
//	@Failure		400		{object}	responses.ErrorBody	"Missing account"
//	@Failure		409		{object}	responses.ErrorBody	"Chain is unsupported"
//	@Failure		422		{object}	responses.ErrorBody	"Passphrase is too short"
//	@Failure		500		{object}	responses.ErrorBody	"Wallet creation failed"
//	@Failure		429		{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/v1/wallets [post]
func (c *WalletController) Store(ctx http.Context) http.Response {
	accountID, _ := requestctx.AccountID(ctx)

	var req walletsrequests.StoreRequest
	defer controllers.DiscardPassphrase(&req.Passphrase)
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	result, err := c.ops.CreateWallet(ctx.Context(), accountID, req.Chain, req.Label, req.Passphrase)
	if err != nil {
		return mapError(ctx, err, "create wallet")
	}

	return ctx.Response().Status(http.StatusCreated).Json(walletsresources.NewCreated(result))
}

// Index godoc
//
//	@Summary		List all wallets
//	@Description	Returns the account wallets with their network (testnet flag) and the native and configured token balances of the last refresh. Testnet wallets carry no USD value.
//	@Tags			Wallets
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		SignatureAuth
//	@Success		200	{object}	controllers.WalletListResponse
//	@Failure		500	{object}	responses.ErrorBody
//	@Failure		429	{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/v1/wallets [get]
func (c *WalletController) Index(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok || accountID == uuid.Nil {
		return mapError(ctx, walletview.ErrAccountRequired, "fetch wallets")
	}
	limit, offset := pagination.ParseParams(ctx, 20)

	page, err := c.view.List(ctx.Context(), walletview.ListInput{
		AccountID: accountID,
		Chain:     ctx.Request().Query("chain"),
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return mapError(ctx, err, "fetch wallets")
	}

	return ctx.Response().Success().Json(pagination.Response(walletsresources.NewListItems(page.Items), page.Total, limit, offset))
}

// Show godoc
//
//	@Summary		Get a wallet
//	@Description	Returns a single wallet by its UUID
//	@Tags			Wallets
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		SignatureAuth
//	@Param			walletId	path		string	true	"Wallet UUID"	format(uuid)
//	@Success		200			{object}	walletresource.WithNetwork
//	@Failure		400			{object}	responses.ErrorBody	"Invalid UUID"
//	@Failure		404			{object}	responses.ErrorBody	"Wallet not found"
//	@Failure		429			{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/v1/wallets/{walletId} [get]
func (c *WalletController) Show(ctx http.Context) http.Response {
	walletID, err := requests.RouteUUID(ctx, "walletId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid wallet id")
	}
	accountID, ok := requestctx.AccountID(ctx)
	if !ok || accountID == uuid.Nil {
		return mapError(ctx, walletview.ErrAccountRequired, "fetch wallet")
	}

	detail, err := c.view.Get(ctx.Context(), walletview.GetInput{AccountID: accountID, WalletID: walletID})
	if err != nil {
		return mapError(ctx, err, "fetch wallet")
	}

	return ctx.Response().Success().Json(walletresource.WithNetworkFrom(detail.Wallet, detail.Network))
}
