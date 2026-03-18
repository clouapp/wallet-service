package controllers

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/google/uuid"

	"github.com/macromarkets/vault/app/container"
)

// GenerateAddress generates a new deposit address for a wallet
func GenerateAddress(ctx http.Context) http.Response {
	walletID, err := uuid.Parse(ctx.Request().Route("id"))
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{
			"error": "invalid wallet id",
		})
	}

	var req struct {
		ExternalUserID string `json:"external_user_id" form:"external_user_id"`
		Metadata       string `json:"metadata" form:"metadata"`
	}
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{
			"error": err.Error(),
		})
	}
	if req.ExternalUserID == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{
			"error": "external_user_id is required",
		})
	}

	addr, err := container.Get().WalletService.GenerateAddress(ctx.Context(), walletID, req.ExternalUserID, req.Metadata)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{
			"error": err.Error(),
		})
	}

	// Refresh Redis address cache for the chain
	if w, err := container.Get().WalletService.GetWallet(ctx.Context(), walletID); err == nil {
		container.Get().DepositService.RefreshAddressCache(ctx.Context(), w.Chain)
	}

	return ctx.Response().Json(http.StatusCreated, addr)
}

// ListWalletAddresses returns all addresses for a wallet
func ListWalletAddresses(ctx http.Context) http.Response {
	walletID, err := uuid.Parse(ctx.Request().Route("id"))
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{
			"error": "invalid wallet id",
		})
	}
	addrs, err := container.Get().WalletService.ListWalletAddresses(ctx.Context(), walletID)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{
			"error": err.Error(),
		})
	}
	return ctx.Response().Success().Json(http.Json{
		"data": addrs,
	})
}

// LookupAddress looks up an address across all chains or a specific chain
func LookupAddress(ctx http.Context) http.Response {
	address := ctx.Request().Route("address")
	chainFilter := ctx.Request().Query("chain", "")

	if chainFilter != "" {
		addr, err := container.Get().WalletService.LookupAddress(ctx.Context(), chainFilter, address)
		if err != nil {
			return ctx.Response().Json(http.StatusNotFound, http.Json{
				"error": "address not found",
			})
		}
		return ctx.Response().Success().Json(addr)
	}

	// Try all chains
	for _, id := range container.Get().Registry.ChainIDs() {
		if addr, err := container.Get().WalletService.LookupAddress(ctx.Context(), id, address); err == nil {
			return ctx.Response().Success().Json(addr)
		}
	}
	return ctx.Response().Json(http.StatusNotFound, http.Json{
		"error": "address not found",
	})
}

// ListUserAddresses returns all addresses for a user
func ListUserAddresses(ctx http.Context) http.Response {
	addrs, err := container.Get().WalletService.ListUserAddresses(ctx.Context(), ctx.Request().Route("external_id"))
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{
			"error": err.Error(),
		})
	}
	return ctx.Response().Success().Json(http.Json{
		"data": addrs,
	})
}
