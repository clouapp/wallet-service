package wallets

import (
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	walletresource "github.com/macrowallets/waas/app/http/resources/dashboard/wallets"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/chainregistry"
	wallet "github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

func validateRequest(ctx http.Context, req http.FormRequest) http.Response {
	return controllers.ValidateRequest(ctx, req)
}

// CreateWalletResponse is the external create response: the wallet fields and
// the combined public key. The customer share, the passphrase, and the service
// share are not on it.
type CreateWalletResponse struct {
	walletresource.Wallet
	// Hex of the combined MPC public key.
	ServicePublicKey string `json:"service_public_key" example:"02a1b2c3..."`
}

func newCreateWalletResponse(result *wallet.CreateWalletResult) CreateWalletResponse {
	return CreateWalletResponse{
		Wallet:           walletresource.WalletFrom(*result.Wallet),
		ServicePublicKey: result.ServicePublicKey,
	}
}

// WalletsController serves the external wallet list, create, and get routes.
type WalletsController struct {
	wallets       *walletrecords.Wallets
	walletService func() *wallet.Service
}

// WalletsControllerDeps is everything the external wallets controller needs.
// Every field is required. WalletService is stored and read on each call.
type WalletsControllerDeps struct {
	Wallets       *walletrecords.Wallets
	WalletService func() *wallet.Service
}

// NewWalletsController wires the external wallet handlers from WalletsControllerDeps.
func NewWalletsController(deps WalletsControllerDeps) *WalletsController {
	if deps.Wallets == nil {
		panic("external wallets controller: wallets service is required")
	}
	if deps.WalletService == nil || deps.WalletService() == nil {
		panic("external wallets controller: wallet service is required")
	}
	return &WalletsController{
		wallets:       deps.Wallets,
		walletService: deps.WalletService,
	}
}

// CreateWallet godoc
// @Summary      Create a new wallet
// @Description  Creates a new HD wallet for the specified blockchain. Only one wallet per chain is allowed.
// @Description  The response carries the wallet fields and `service_public_key`.
// @Tags         Wallets
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        body  body      CreateWalletSwagger  true  "Wallet creation request"
// @Success      201   {object}  CreateWalletResponse
// @Failure      400   {object}  ErrorResponse  "Missing account"
// @Failure      409   {object}  ErrorResponse  "Chain is unsupported"
// @Failure      422   {object}  ErrorResponse  "Passphrase is too short"
// @Failure      500   {object}  ErrorResponse  "Wallet creation failed"
// @Router       /v1/wallets [post]
func (ctrl *WalletsController) CreateWallet(ctx http.Context) http.Response {
	var req requests.CreateWalletRequest
	defer controllers.DiscardPassphrase(&req.Passphrase)
	if resp := validateRequest(ctx, &req); resp != nil {
		return resp
	}

	accountID, _ := requestctx.AccountID(ctx)
	result, err := ctrl.walletService().CreateWallet(ctx.Context(), accountID, req.Chain, req.Label, req.Passphrase)
	if err != nil {
		return mapCreateWalletError(ctx, err)
	}
	if result == nil || result.Wallet == nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{
			"error": "wallet service returned no wallet",
		})
	}
	return responses.Send(ctx, http.StatusCreated, newCreateWalletResponse(result))
}

// mapCreateWalletError answers a CreateWallet failure. A 4xx is a failure the
// caller can fix. Anything else is an outage: 500, with the cause logged and
// kept out of the body. One 409 for every error hid that outage behind a
// message about the caller.
func mapCreateWalletError(ctx http.Context, err error) http.Response {
	switch {
	case errors.Is(err, chainregistry.ErrUnknownChain):
		return responses.Send(ctx, http.StatusConflict, http.Json{"error": "unknown chain"})
	case err.Error() == "passphrase must be at least 12 characters":
		return responses.Send(ctx, http.StatusUnprocessableEntity, http.Json{"error": "passphrase must be at least 12 characters"})
	case err.Error() == "account_id is required":
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "account_id is required"})
	default:
		return responses.InternalError(ctx, err)
	}
}

// ListWallets godoc
// @Summary      List all wallets
// @Description  Returns the account wallets with their network (testnet flag) and the native and configured token balances of the last refresh. Testnet wallets carry no USD value.
// @Tags         Wallets
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Success      200  {object}  WalletListResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /v1/wallets [get]
func (ctrl *WalletsController) ListWallets(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok || accountID == uuid.Nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{
			"error": "account is required",
		})
	}

	limit, offset := pagination.ParseParams(ctx, 20)
	var query requests.ListWalletsRequest
	query.Load(ctx)
	chain := query.Chain

	wallets, total, err := ctrl.wallets.PaginateByAccount(ctx.Context(), accountID, chain, limit, offset)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{
			"error": "failed to fetch wallets",
		})
	}
	items, err := controllers.LoadWalletListItems(ctx.Context(), wallets)
	if err != nil {
		slog.Error("load wallet list balances", "account", accountID, "error", err)
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{
			"error": "failed to fetch wallet balances",
		})
	}
	return responses.Send(ctx, http.StatusOK, pagination.Response(items, total, limit, offset))
}

// GetWallet godoc
// @Summary      Get a wallet
// @Description  Returns a single wallet by its UUID
// @Tags         Wallets
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        walletId   path      string  true  "Wallet UUID"  format(uuid)
// @Success      200  {object}  walletresource.WithNetwork
// @Failure      400  {object}  ErrorResponse  "Invalid UUID"
// @Failure      404  {object}  ErrorResponse  "Wallet not found"
// @Router       /v1/wallets/{walletId} [get]
func (ctrl *WalletsController) GetWallet(ctx http.Context) http.Response {
	id, err := requests.RouteUUID(ctx, "walletId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{
			"error": "invalid wallet id",
		})
	}

	accountID, ok := requestctx.AccountID(ctx)
	if !ok || accountID == uuid.Nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{
			"error": "account is required",
		})
	}

	w, err := ctrl.wallets.FindByIDAndAccount(ctx.Context(), id, accountID)
	if err != nil || w == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{
			"error": "wallet not found",
		})
	}
	return ctx.Response().Success().Json(walletresource.WithNetworkFrom(w, controllers.ResolveWalletChainNetwork(ctx.Context(), w.Chain)))
}

// CreateWalletSwagger is the request body for creating a wallet.
type CreateWalletSwagger struct {
	Chain      string `json:"chain" example:"eth"`
	Label      string `json:"label" example:"My Ethereum Wallet"`
	Passphrase string `json:"passphrase" example:"my-secret-passphrase-12chars"`
}
