package controllers

import (
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/models"
	wallet "github.com/macrowallets/waas/app/services/wallet"
)

// CreateWallet godoc
// @Summary      Create a new wallet
// @Description  Creates a new HD wallet for the specified blockchain. Only one wallet per chain is allowed.
// @Tags         Wallets
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        body  body      CreateWalletRequest  true  "Wallet creation request"
// @Success      201   {object}  models.Wallet
// @Failure      400   {object}  ErrorResponse  "Missing or invalid fields"
// @Failure      409   {object}  ErrorResponse  "Wallet for this chain already exists or chain is unsupported"
// @Router       /v1/wallets [post]
func CreateWallet(ctx http.Context) http.Response {
	var req requests.CreateWalletRequest
	validationErrors, err := ctx.Request().ValidateRequest(&req)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": err.Error()})
	}
	if validationErrors != nil {
		return ctx.Response().Json(http.StatusUnprocessableEntity, validationErrors.All())
	}

	result, err := container.Get().WalletService.CreateWallet(ctx.Context(), req.Chain, req.Label, req.Passphrase)
	if err != nil {
		return ctx.Response().Json(http.StatusConflict, http.Json{
			"error": err.Error(),
		})
	}
	return ctx.Response().Json(http.StatusCreated, result.Wallet)
}

// ListWallets godoc
// @Summary      List all wallets
// @Description  Returns all wallets across all supported chains
// @Tags         Wallets
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Success      200  {object}  WalletListResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /v1/wallets [get]
func ListWallets(ctx http.Context) http.Response {
	limit, offset := pagination.ParseParams(ctx, 20)
	wallets, total, err := container.Get().WalletRepo.PaginateAll(limit, offset)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{
			"error": "failed to fetch wallets",
		})
	}
	return ctx.Response().Json(http.StatusOK, pagination.Response(wallets, total, limit, offset))
}

// GetWallet godoc
// @Summary      Get a wallet
// @Description  Returns a single wallet by its UUID
// @Tags         Wallets
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        walletId   path      string  true  "Wallet UUID"  format(uuid)
// @Success      200  {object}  models.Wallet
// @Failure      400  {object}  ErrorResponse  "Invalid UUID"
// @Failure      404  {object}  ErrorResponse  "Wallet not found"
// @Router       /v1/wallets/{walletId} [get]
func GetWallet(ctx http.Context) http.Response {
	id, err := uuid.Parse(ctx.Request().Route("walletId"))
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{
			"error": "invalid wallet id",
		})
	}
	w, err := container.Get().WalletService.GetWallet(ctx.Context(), id)
	if err != nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{
			"error": "wallet not found",
		})
	}
	return ctx.Response().Success().Json(w)
}

// CreateWalletAdmin creates a wallet from the admin panel with full MPC keygen.
// Returns keycard data including activation_code for the two-step setup flow.
func CreateWalletAdmin(ctx http.Context) http.Response {
	var req struct {
		Chain             string `json:"chain"`
		Label             string `json:"label"`
		Passphrase        string `json:"passphrase"`
		ConfirmPassphrase string `json:"confirm_passphrase"`
	}
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
	if req.Chain == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "chain is required"})
	}
	if req.Label == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "label is required"})
	}
	if len(req.Passphrase) < 12 {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "passphrase must be at least 12 characters"})
	}
	if req.Passphrase != req.ConfirmPassphrase {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "passphrases do not match"})
	}

	if env, ok := ctx.Value("account_environment").(string); ok && env != "" {
		chainRecord, chainErr := container.Get().ChainRepo.FindByID(req.Chain)
		if chainErr != nil || chainRecord == nil {
			return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "unsupported chain"})
		}
		if chainRecord.IsTestnet != (env == models.EnvironmentTest) {
			return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "chain not available in current environment"})
		}
	}

	result, err := container.Get().WalletService.CreateWallet(ctx.Context(), req.Chain, req.Label, req.Passphrase)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "unknown chain") {
			return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": msg})
		}
		if strings.Contains(msg, "already exists") {
			return ctx.Response().Json(http.StatusConflict, http.Json{"error": msg})
		}
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": msg})
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
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid wallet id"})
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}

	_, err = container.Get().WalletService.ActivateWallet(ctx.Context(), walletID, req.Code)
	if err != nil {
		switch {
		case errors.Is(err, wallet.ErrWalletNotFound):
			return ctx.Response().Json(http.StatusNotFound, http.Json{"error": err.Error()})
		case errors.Is(err, wallet.ErrWalletAlreadyActive):
			return ctx.Response().Json(http.StatusConflict, http.Json{"error": err.Error()})
		case errors.Is(err, wallet.ErrInvalidActivationCode):
			return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": err.Error()})
		default:
			return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "internal error"})
		}
	}

	return ctx.Response().Json(http.StatusOK, http.Json{"status": "active"})
}

// CreateWalletRequest is the request body for creating a wallet.
type CreateWalletRequest struct {
	Chain      string `json:"chain" example:"eth"`
	Label      string `json:"label" example:"My Ethereum Wallet"`
	Passphrase string `json:"passphrase" example:"my-secret-passphrase-12chars"`
}

// ensure models import is used
var _ models.Wallet
