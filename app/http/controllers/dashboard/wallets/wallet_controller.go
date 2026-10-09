package wallets

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	dashwalletsrequests "github.com/macrowallets/waas/app/http/requests/dashboard/wallets"
	walletsrequests "github.com/macrowallets/waas/app/http/requests/wallets"
	walletresource "github.com/macrowallets/waas/app/http/resources/dashboard/wallets"
	walletsresources "github.com/macrowallets/waas/app/http/resources/wallets"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/walletops"
	"github.com/macrowallets/waas/app/services/walletview"
)

// WalletController serves the dashboard wallet list, create, get and activate routes.
type WalletController struct {
	view *walletview.Service
	ops  *walletops.Service
}

// NewWalletController wires the controller with the wallet reads and the wallet
// operations.
func NewWalletController(view *walletview.Service, ops *walletops.Service) *WalletController {
	switch {
	case view == nil:
		panic("dashboard wallets controller: wallet view is required")
	case ops == nil:
		panic("dashboard wallets controller: wallet operations are required")
	}
	return &WalletController{view: view, ops: ops}
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
		Caller:    callerOf(ctx),
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

	detail, err := c.view.Get(ctx.Context(), walletview.GetInput{
		AccountID: accountID,
		WalletID:  walletID,
		Caller:    callerOf(ctx),
	})
	if err != nil {
		return mapError(ctx, err, "fetch wallet")
	}

	return ctx.Response().Success().Json(walletresource.WithNetworkFrom(detail.Wallet, detail.Network))
}

// Store creates a wallet from the admin panel with full MPC keygen. The
// response carries the wallet, the combined public key, and the activation
// code. The customer share, the passphrase, and the service share are not on it.
func (c *WalletController) Store(ctx http.Context) http.Response {
	accountID, _ := requestctx.AccountID(ctx)
	environment, _ := requestctx.AccountEnvironment(ctx)

	var req walletsrequests.StoreRequest
	defer controllers.DiscardPassphrase(&req.Passphrase)
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	result, err := c.ops.CreateWalletInEnvironment(ctx.Context(), walletops.CreateWalletInput{
		AccountID:   accountID,
		Environment: environment,
		Chain:       req.Chain,
		Label:       req.Label,
		Passphrase:  req.Passphrase,
	})
	if err != nil {
		return mapError(ctx, err, "create wallet")
	}

	return ctx.Response().Status(http.StatusCreated).Json(walletsresources.NewCreation(result))
}

// Activate confirms the user has saved their KeyCard by validating the activation code.
func (c *WalletController) Activate(ctx http.Context) http.Response {
	walletID, err := requests.RouteUUID(ctx, "walletId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid wallet id")
	}

	var req dashwalletsrequests.ActivateRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	if err := c.ops.ActivateWallet(ctx.Context(), walletID, req.Code); err != nil {
		return mapError(ctx, err, actionActivate)
	}

	return ctx.Response().Success().Json(http.Json{"status": "active"})
}

// callerOf is the member asking, as the scope middleware stored them; the
// wallet view decides which wallets they see.
func callerOf(ctx http.Context) *walletview.Caller {
	account, _ := requestctx.Account(ctx)
	role, _ := requestctx.AccountRole(ctx)
	userID, _ := requestctx.UserID(ctx)
	return &walletview.Caller{Account: account, Role: role, UserID: userID}
}
