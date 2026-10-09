package controllers

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	addressesrequests "github.com/macrowallets/waas/app/http/requests/addresses"
	addressresource "github.com/macrowallets/waas/app/http/resources/addresses"
	walletresource "github.com/macrowallets/waas/app/http/resources/dashboard/wallets"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/walletops"
)

// AddressesHandler serves the address routes both HTTP surfaces (dashboard and
// external) share: generate, update and list by wallet.
type AddressesHandler struct {
	ops *walletops.Service
}

// NewAddressesHandler wires the shared address handlers. surface ("dashboard"
// or "external") only names the surface in the panic message.
func NewAddressesHandler(surface string, ops *walletops.Service) *AddressesHandler {
	if ops == nil {
		panic(surface + " addresses controller: wallet operations are required")
	}
	return &AddressesHandler{ops: ops}
}

// Store godoc
//
//	@Summary		Generate a deposit address
//	@Description	Derives a new deposit address for a user from the wallet's HD key. Each call produces a unique address.
//	@Tags			Addresses
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		SignatureAuth
//	@Param			id		path		string					true	"Wallet UUID"	format(uuid)
//	@Param			body	body		GenerateAddressRequest	true	"Address generation request"
//	@Success		201		{object}	addressresource.Address
//	@Failure		400		{object}	responses.ErrorBody	"Invalid wallet ID or missing fields"
//	@Failure		422		{object}	responses.ErrorBody	"Address generation not supported for MPC wallets"
//	@Failure		500		{object}	responses.ErrorBody
//	@Failure		429		{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/v1/wallets/{id}/addresses [post]
func (c *AddressesHandler) Store(ctx http.Context) http.Response {
	walletID, err := requests.RouteUUID(ctx, "walletId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid wallet id")
	}

	var req addressesrequests.StoreRequest
	defer DiscardPassphrase(&req.Passphrase)
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	address, err := c.ops.GenerateAddress(ctx.Context(), walletops.GenerateAddressInput{
		WalletID:       walletID,
		ExternalUserID: req.ExternalUserID,
		Label:          req.Label,
		Metadata:       req.Metadata,
		Passphrase:     req.Passphrase,
	})
	if err != nil {
		return mapAddressError(ctx, err, "generate address")
	}

	return ctx.Response().Status(http.StatusCreated).Json(addressresource.AddressPtr(address, walletresource.WalletPtr))
}

// Update godoc
//
//	@Summary		Update an address
//	@Description	Updates the label and/or external_user_id of an existing address
//	@Tags			Addresses
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			walletId	path		string							true	"Wallet UUID"	format(uuid)
//	@Param			addressId	path		string							true	"Address UUID"	format(uuid)
//	@Param			body		body		addressesrequests.UpdateRequest	true	"Fields to update"
//	@Success		200			{object}	addressresource.Address
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Failure		500			{object}	responses.ErrorBody
//	@Failure		429			{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/v1/wallets/{walletId}/addresses/{addressId} [patch]
func (c *AddressesHandler) Update(ctx http.Context) http.Response {
	addressID, err := requests.RouteUUID(ctx, "addressId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid address id")
	}

	var req addressesrequests.UpdateRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	address, err := c.ops.UpdateAddress(ctx.Context(), walletops.UpdateAddressInput{
		AddressID:      addressID,
		Label:          req.Label,
		ExternalUserID: req.ExternalUserID,
	})
	if err != nil {
		return mapAddressError(ctx, err, "update address")
	}

	return ctx.Response().Success().Json(addressresource.AddressPtr(address, walletresource.WalletPtr))
}

// Index godoc
//
//	@Summary		List wallet addresses
//	@Description	Returns all deposit addresses generated for a specific wallet
//	@Tags			Addresses
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		SignatureAuth
//	@Param			id	path		string	true	"Wallet UUID"	format(uuid)
//	@Success		200	{object}	AddressListResponse
//	@Failure		400	{object}	responses.ErrorBody	"Invalid wallet UUID"
//	@Failure		500	{object}	responses.ErrorBody
//	@Failure		429	{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/v1/wallets/{id}/addresses [get]
func (c *AddressesHandler) Index(ctx http.Context) http.Response {
	walletID, err := requests.RouteUUID(ctx, "walletId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid wallet id")
	}
	limit, offset := pagination.ParseParams(ctx, 20)

	addresses, total, err := c.ops.ListAddresses(ctx.Context(), walletID, limit, offset)
	if err != nil {
		return mapAddressError(ctx, err, "list addresses")
	}

	return ctx.Response().Success().Json(pagination.Response(addressresource.AddressesFrom(addresses, walletresource.WalletPtr), total, limit, offset))
}
