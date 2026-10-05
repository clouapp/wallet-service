package wallets

import (
	"log/slog"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	walletresource "github.com/macrowallets/waas/app/http/resources/dashboard/wallets"
	"github.com/macrowallets/waas/app/http/responses"
	wallet "github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

func validateRequest(ctx http.Context, req http.FormRequest) http.Response {
	return controllers.ValidateRequest(ctx, req)
}

// CreateWalletResponse is the external create response: the wallet fields plus
// the KeyCard recovery material, which is returned only here, once. The service
// share (share B) and the plaintext customer share are never part of it.
type CreateWalletResponse struct {
	walletresource.Wallet
	// JSON {iv,salt,ct,cipher,kdf}: the customer share (share A) encrypted with the wallet passphrase (AES-256-GCM, Argon2id), base64 fields.
	EncryptedUserKey string `json:"encrypted_user_key" example:"{\"iv\":\"...\",\"salt\":\"...\",\"ct\":\"...\",\"cipher\":\"aes-256-gcm\",\"kdf\":\"argon2id\"}"`
	// Hex of the combined MPC public key.
	ServicePublicKey string `json:"service_public_key" example:"02a1b2c3..."`
}

func newCreateWalletResponse(result *wallet.CreateWalletResult) CreateWalletResponse {
	return CreateWalletResponse{
		Wallet:           walletresource.WalletFrom(*result.Wallet),
		EncryptedUserKey: result.EncryptedUserKey,
		ServicePublicKey: result.ServicePublicKey,
	}
}

// WalletsController serves the external wallet list, create, and get routes.
type WalletsController struct {
	wallets       *walletrecords.Wallets
	walletService func() *wallet.Service
}

func NewWalletsController(
	wallets *walletrecords.Wallets,
	walletService func() *wallet.Service,
) *WalletsController {
	if wallets == nil {
		panic("external wallets controller: wallets service is required")
	}
	if walletService == nil || walletService() == nil {
		panic("external wallets controller: wallet service is required")
	}
	return &WalletsController{
		wallets:       wallets,
		walletService: walletService,
	}
}

// CreateWallet godoc
// @Summary      Create a new wallet
// @Description  Creates a new HD wallet for the specified blockchain. Only one wallet per chain is allowed.
// @Description  The response carries the wallet fields plus the one-time KeyCard recovery material:
// @Description  `encrypted_user_key` (the customer MPC share encrypted with the passphrase) and
// @Description  `service_public_key`. Store them securely — no other endpoint ever returns them again.
// @Tags         Wallets
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        body  body      CreateWalletSwagger  true  "Wallet creation request"
// @Success      201   {object}  CreateWalletResponse
// @Failure      400   {object}  ErrorResponse  "Missing or invalid fields"
// @Failure      409   {object}  ErrorResponse  "Wallet for this chain already exists or chain is unsupported"
// @Failure      500   {object}  ErrorResponse  "Wallet service returned no wallet"
// @Router       /v1/wallets [post]
func (ctrl *WalletsController) CreateWallet(ctx http.Context) http.Response {
	var req requests.CreateWalletRequest
	if resp := validateRequest(ctx, &req); resp != nil {
		return resp
	}

	accountID, _ := requestctx.AccountID(ctx)
	result, err := ctrl.walletService().CreateWallet(ctx.Context(), accountID, req.Chain, req.Label, req.Passphrase)
	if err != nil {
		return responses.Send(ctx, http.StatusConflict, http.Json{
			"error": err.Error(),
		})
	}
	if result == nil || result.Wallet == nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{
			"error": "wallet service returned no wallet",
		})
	}
	return responses.Send(ctx, http.StatusCreated, newCreateWalletResponse(result))
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
