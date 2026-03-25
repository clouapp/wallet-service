package controllers

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
)

// GenerateAddress godoc
// @Summary      Generate a deposit address
// @Description  Derives a new deposit address for a user from the wallet's HD key. Each call produces a unique address.
// @Tags         Addresses
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        id    path      string                  true  "Wallet UUID"  format(uuid)
// @Param        body  body      GenerateAddressRequest  true  "Address generation request"
// @Success      201   {object}  models.Address
// @Failure      400   {object}  ErrorResponse  "Invalid wallet ID or missing fields"
// @Failure      422   {object}  ErrorResponse  "Address generation not supported for MPC wallets"
// @Failure      500   {object}  ErrorResponse
// @Router       /v1/wallets/{id}/addresses [post]
func GenerateAddress(ctx http.Context) http.Response {
	walletID, err := uuid.Parse(ctx.Request().Route("walletId"))
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{
			"error": "invalid wallet id",
		})
	}

	var req requests.GenerateAddressRequest
	validationErrors, err := ctx.Request().ValidateRequest(&req)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": err.Error()})
	}
	if validationErrors != nil {
		return ctx.Response().Json(http.StatusUnprocessableEntity, validationErrors.All())
	}

	addr, err := container.Get().WalletService.GenerateAddress(ctx.Context(), walletID, req.ExternalUserID, req.Metadata)
	if err != nil {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{
			"error": err.Error(),
		})
	}

	// Refresh Redis address cache for the chain
	if w, err := container.Get().WalletService.GetWallet(ctx.Context(), walletID); err == nil {
		container.Get().DepositService.RefreshAddressCache(ctx.Context(), w.Chain)
	}

	return ctx.Response().Json(http.StatusCreated, addr)
}

// ListWalletAddresses godoc
// @Summary      List wallet addresses
// @Description  Returns all deposit addresses generated for a specific wallet
// @Tags         Addresses
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        id  path      string  true  "Wallet UUID"  format(uuid)
// @Success      200  {object}  AddressListResponse
// @Failure      400  {object}  ErrorResponse  "Invalid wallet UUID"
// @Failure      500  {object}  ErrorResponse
// @Router       /v1/wallets/{id}/addresses [get]
func ListWalletAddresses(ctx http.Context) http.Response {
	walletID, err := uuid.Parse(ctx.Request().Route("walletId"))
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{
			"error": "invalid wallet id",
		})
	}
	limit, offset := pagination.ParseParams(ctx, 20)
	addrs, total, err := container.Get().AddressRepo.PaginateByWalletID(walletID, limit, offset)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{
			"error": "failed to fetch addresses",
		})
	}
	return ctx.Response().Json(http.StatusOK, pagination.Response(addrs, total, limit, offset))
}

// LookupAddress godoc
// @Summary      Look up an address
// @Description  Finds a deposit address record by its on-chain address string. Optionally filter by chain.
// @Tags         Addresses
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        address  path      string  true   "On-chain address"  example("0xABCDEF1234567890")
// @Param        chain    query     string  false  "Chain ID filter"   example("eth")
// @Success      200      {object}  models.Address
// @Failure      400      {object}  ErrorResponse  "Missing chain parameter"
// @Failure      404      {object}  ErrorResponse  "Address not found"
// @Router       /v1/addresses/{address} [get]
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

// ListUserAddresses godoc
// @Summary      List addresses for a user
// @Description  Returns all deposit addresses assigned to a specific external user ID across all chains
// @Tags         Addresses
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        external_id  path      string  true  "External user identifier"  example("user_123")
// @Success      200          {object}  AddressListResponse
// @Failure      500          {object}  ErrorResponse
// @Router       /v1/users/{external_id}/addresses [get]
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

// GenerateAddressRequest is the request body for generating a deposit address.
type GenerateAddressRequest struct {
	ExternalUserID string `json:"external_user_id" example:"user_123"`
	Metadata       string `json:"metadata"          example:"{\"tier\":\"premium\"}"`
}
