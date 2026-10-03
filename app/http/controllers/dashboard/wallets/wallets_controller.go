package wallets

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	wallet "github.com/macrowallets/waas/app/services/wallet"
)

func validateRequest(ctx http.Context, req http.FormRequest) http.Response {
	return controllers.ValidateRequest(ctx, req)
}

func authorize(ctx http.Context, ability string, arguments map[string]any) http.Response {
	return controllers.Authorize(ctx, ability, arguments)
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
func ListWallets(ctx http.Context) http.Response {
	accountID, ok := ctx.Value("account_id").(uuid.UUID)
	if !ok || accountID == uuid.Nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{
			"error": "account is required",
		})
	}

	limit, offset := pagination.ParseParams(ctx, 20)
	chain := ctx.Request().Query("chain", "")

	wallets, total, err := container.MustMake[*repositories.WalletRepository]().PaginateByAccount(ctx.Context(), accountID, chain, limit, offset)
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
	return ctx.Response().Json(http.StatusOK, pagination.Response(items, total, limit, offset))
}

// GetWallet godoc
// @Summary      Get a wallet
// @Description  Returns a single wallet by its UUID
// @Tags         Wallets
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        walletId   path      string  true  "Wallet UUID"  format(uuid)
// @Success      200  {object}  WalletView
// @Failure      400  {object}  ErrorResponse  "Invalid UUID"
// @Failure      404  {object}  ErrorResponse  "Wallet not found"
// @Router       /v1/wallets/{walletId} [get]
func GetWallet(ctx http.Context) http.Response {
	id, err := uuid.Parse(ctx.Request().Route("walletId"))
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{
			"error": "invalid wallet id",
		})
	}

	accountID, ok := ctx.Value("account_id").(uuid.UUID)
	if !ok || accountID == uuid.Nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{
			"error": "account is required",
		})
	}

	w, err := container.MustMake[*repositories.WalletRepository]().FindByIDAndAccount(ctx.Context(), id, accountID)
	if err != nil || w == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{
			"error": "wallet not found",
		})
	}
	return ctx.Response().Success().Json(controllers.NewWalletView(w, controllers.ResolveWalletChainNetwork(ctx.Context(), w.Chain)))
}

// CreateWalletAdmin creates a wallet from the admin panel with full MPC keygen.
// Returns keycard data including activation_code for the two-step setup flow.
func CreateWalletAdmin(ctx http.Context) http.Response {
	var req requests.CreateWalletAdminRequest
	if resp := validateRequest(ctx, &req); resp != nil {
		return resp
	}

	if env, ok := ctx.Value("account_environment").(string); ok && env != "" {
		chainRecord, _ := container.MustMake[*repositories.ChainRepository]().FindByID(ctx.Context(), req.Chain)
		if chainRecord != nil && chainRecord.IsTestnet != (env == models.EnvironmentTest) {
			return responses.Send(ctx, http.StatusForbidden, http.Json{"error": "chain not available in current environment"})
		}
	}

	accountID, _ := ctx.Value("account_id").(uuid.UUID)
	result, err := container.Get().WalletService.CreateWallet(ctx.Context(), accountID, req.Chain, req.Label, req.Passphrase)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "unknown chain") {
			return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": msg})
		}
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": msg})
	}

	return ctx.Response().Json(http.StatusCreated, http.Json{
		"wallet":             result.Wallet,
		"encrypted_user_key": result.EncryptedUserKey,
		"service_public_key": result.ServicePublicKey,
		"encrypted_passcode": result.EncryptedPasscode,
		"activation_code":    result.ActivationCode,
	})
}

// ActivateWallet confirms the user has saved their KeyCard by validating the activation code.
func ActivateWallet(ctx http.Context) http.Response {
	walletID, err := uuid.Parse(ctx.Request().Route("walletId"))
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid wallet id"})
	}

	var req requests.ActivateWalletRequest
	if resp := validateRequest(ctx, &req); resp != nil {
		return resp
	}

	_, err = container.Get().WalletService.ActivateWallet(ctx.Context(), walletID, req.Code)
	if err != nil {
		switch {
		case errors.Is(err, wallet.ErrWalletNotFound):
			return responses.Send(ctx, http.StatusNotFound, http.Json{"error": err.Error()})
		case errors.Is(err, wallet.ErrWalletAlreadyActive):
			return responses.Send(ctx, http.StatusConflict, http.Json{"error": err.Error()})
		case errors.Is(err, wallet.ErrInvalidActivationCode):
			return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": err.Error()})
		default:
			return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal error"})
		}
	}

	return ctx.Response().Json(http.StatusOK, http.Json{"status": "active"})
}
