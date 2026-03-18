package controllers

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/google/uuid"

	"github.com/macromarkets/vault/app/container"
)

// CreateWallet creates a new wallet for a specific blockchain
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

// ListWallets returns all wallets
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

// GetWallet returns a single wallet by ID
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
