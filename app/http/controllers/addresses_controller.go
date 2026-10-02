package controllers

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
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
		return responses.Send(ctx, http.StatusBadRequest, http.Json{
			"error": "invalid wallet id",
		})
	}

	var req requests.GenerateAddressRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	addr, err := container.Get().WalletService.GenerateAddress(ctx.Context(), walletID, req.ExternalUserID, req.Label, req.Metadata, req.Passphrase)
	if err != nil {
		return responses.Send(ctx, http.StatusUnprocessableEntity, http.Json{
			"error": err.Error(),
		})
	}

	// Refresh Redis address cache for the chain
	if w, err := container.Get().WalletService.GetWallet(ctx.Context(), walletID); err == nil {
		container.Get().DepositService.RefreshAddressCache(ctx.Context(), w.Chain)
	}

	return ctx.Response().Json(http.StatusCreated, addr)
}

// UpdateAddress godoc
// @Summary      Update an address
// @Description  Updates the label and/or external_user_id of an existing address
// @Tags         Addresses
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Param        walletId   path      string                  true  "Wallet UUID"  format(uuid)
// @Param        addressId  path      string                  true  "Address UUID" format(uuid)
// @Param        body       body      requests.UpdateAddressRequest    true  "Fields to update"
// @Success      200        {object}  models.Address
// @Failure      400        {object}  ErrorResponse
// @Failure      404        {object}  ErrorResponse
// @Failure      500        {object}  ErrorResponse
// @Router       /v1/wallets/{walletId}/addresses/{addressId} [patch]
func UpdateAddress(ctx http.Context) http.Response {
	addressID, err := uuid.Parse(ctx.Request().Route("addressId"))
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{
			"error": "invalid address id",
		})
	}

	var req requests.UpdateAddressRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	fields := make(map[string]interface{})
	if req.Label != nil {
		fields["label"] = *req.Label
	}
	if req.ExternalUserID != nil {
		fields["external_user_id"] = *req.ExternalUserID
	}

	if len(fields) == 0 {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{
			"error": "no fields to update",
		})
	}

	addr, err := container.Get().WalletService.UpdateAddress(ctx.Context(), addressID, fields)
	if err != nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{
			"error": err.Error(),
		})
	}

	return ctx.Response().Success().Json(addr)
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
		return responses.Send(ctx, http.StatusBadRequest, http.Json{
			"error": "invalid wallet id",
		})
	}
	limit, offset := pagination.ParseParams(ctx, 20)
	addrs, total, err := container.Get().AddressRepo.PaginateByWalletID(ctx.Context(), walletID, limit, offset)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{
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
	accountID, ok := ctx.Value("account_id").(uuid.UUID)
	if !ok {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{
			"error": "unauthorized",
		})
	}

	address := ctx.Request().Route("address")
	chainFilter := ctx.Request().Query("chain", "")

	if chainFilter != "" {
		addr, err := container.Get().WalletService.LookupAddressForAccount(ctx.Context(), chainFilter, address, accountID)
		if err != nil || addr == nil {
			return responses.Send(ctx, http.StatusNotFound, http.Json{
				"error": "address not found",
			})
		}
		return ctx.Response().Success().Json(addr)
	}

	// Try all chains — still scoped to the caller's account so a hit on any
	// chain that belongs to a different account does not leak.
	for _, id := range container.Get().Registry.ChainIDs() {
		addr, err := container.Get().WalletService.LookupAddressForAccount(ctx.Context(), id, address, accountID)
		if err == nil && addr != nil {
			return ctx.Response().Success().Json(addr)
		}
	}
	return responses.Send(ctx, http.StatusNotFound, http.Json{
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
	accountID, ok := ctx.Value("account_id").(uuid.UUID)
	if !ok {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{
			"error": "unauthorized",
		})
	}

	addrs, err := container.Get().WalletService.ListUserAddressesForAccount(
		ctx.Context(),
		ctx.Request().Route("external_id"),
		accountID,
	)
	if err != nil {
		return MapInternalError(ctx, err, "list_user_addresses")
	}
	// An empty slice is the honest response for both "no such external_id"
	// and "external_id exists under another account". Do not distinguish.
	return ctx.Response().Success().Json(http.Json{
		"data": addrs,
	})
}

// GenerateAddressRequest is the request body for generating a deposit address.
type GenerateAddressRequest struct {
	ExternalUserID string `json:"external_user_id" example:"user_123"`
	Metadata       string `json:"metadata"          example:"{\"tier\":\"premium\"}"`
}
