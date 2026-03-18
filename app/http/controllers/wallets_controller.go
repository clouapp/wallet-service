package controllers

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macromarkets/vault/app/container"
	"github.com/macromarkets/vault/app/models"
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
	var req struct {
		Chain string `json:"chain" form:"chain"`
		Label string `json:"label" form:"label"`
	}
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{
			"error": err.Error(),
		})
	}
	if req.Chain == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{
			"error": "chain is required",
		})
	}

	w, err := container.Get().WalletService.CreateWallet(ctx.Context(), req.Chain, req.Label)
	if err != nil {
		return ctx.Response().Json(http.StatusConflict, http.Json{
			"error": err.Error(),
		})
	}
	return ctx.Response().Json(http.StatusCreated, w)
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
	ws, err := container.Get().WalletService.ListWallets(ctx.Context())
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{
			"error": err.Error(),
		})
	}
	return ctx.Response().Success().Json(http.Json{
		"data": ws,
	})
}

// GetWallet godoc
// @Summary      Get a wallet
// @Description  Returns a single wallet by its UUID
// @Tags         Wallets
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        id   path      string  true  "Wallet UUID"  format(uuid)
// @Success      200  {object}  models.Wallet
// @Failure      400  {object}  ErrorResponse  "Invalid UUID"
// @Failure      404  {object}  ErrorResponse  "Wallet not found"
// @Router       /v1/wallets/{id} [get]
func GetWallet(ctx http.Context) http.Response {
	id, err := uuid.Parse(ctx.Request().Route("id"))
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

// CreateWalletRequest is the request body for creating a wallet.
type CreateWalletRequest struct {
	Chain string `json:"chain" example:"eth"`
	Label string `json:"label" example:"My Ethereum Wallet"`
}

// ensure models import is used
var _ models.Wallet
