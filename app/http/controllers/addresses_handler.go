package controllers

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	addressresource "github.com/macrowallets/waas/app/http/resources/addresses"
	walletresource "github.com/macrowallets/waas/app/http/resources/dashboard/wallets"
	"github.com/macrowallets/waas/app/http/responses"
	deposit "github.com/macrowallets/waas/app/services/deposit"
	wallet "github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// AddressesHandler serves the address routes both HTTP surfaces (dashboard and
// external) share: generate, update and list by wallet.
type AddressesHandler struct {
	addresses     *walletrecords.Addresses
	walletService func() *wallet.Service
	deposits      *deposit.Service
}

// AddressesHandlerDeps is everything the addresses handler needs.
// Every field is required.
type AddressesHandlerDeps struct {
	Addresses     *walletrecords.Addresses
	WalletService func() *wallet.Service
	Deposits      *deposit.Service
}

// NewAddressesHandler wires the shared address handlers from AddressesHandlerDeps.
// surface ("dashboard" or "external") only names the surface in the panic messages.
func NewAddressesHandler(surface string, deps AddressesHandlerDeps) *AddressesHandler {
	if deps.Addresses == nil {
		panic(surface + " addresses controller: addresses service is required")
	}
	if deps.WalletService == nil || deps.WalletService() == nil {
		panic(surface + " addresses controller: wallet service is required")
	}
	if deps.Deposits == nil {
		panic(surface + " addresses controller: deposit service is required")
	}
	return &AddressesHandler{
		addresses:     deps.Addresses,
		walletService: deps.WalletService,
		deposits:      deps.Deposits,
	}
}

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
// @Success      201   {object}  addressresource.Address
// @Failure      400   {object}  ErrorResponse  "Invalid wallet ID or missing fields"
// @Failure      422   {object}  ErrorResponse  "Address generation not supported for MPC wallets"
// @Failure      500   {object}  ErrorResponse
// @Router       /v1/wallets/{id}/addresses [post]
func (ctrl *AddressesHandler) GenerateAddress(ctx http.Context) http.Response {
	walletID, err := requests.RouteUUID(ctx, "walletId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid wallet id")
	}

	var req requests.GenerateAddressRequest
	defer DiscardPassphrase(&req.Passphrase)
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}

	addr, err := ctrl.walletService().GenerateAddress(ctx.Context(), walletID, req.ExternalUserID, req.Label, req.Metadata, req.Passphrase)
	if err != nil {
		return AddressGenerationError(ctx, err)
	}

	// Refresh Redis address cache for the chain
	if w, err := ctrl.walletService().GetWallet(ctx.Context(), walletID); err == nil {
		ctrl.deposits.RefreshAddressCache(ctx.Context(), w.Chain)
	}

	return ctx.Response().Status(http.StatusCreated).Json(addressresource.AddressPtr(addr, walletresource.WalletPtr))
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
// @Success      200        {object}  addressresource.Address
// @Failure      400        {object}  ErrorResponse
// @Failure      404        {object}  ErrorResponse
// @Failure      500        {object}  ErrorResponse
// @Router       /v1/wallets/{walletId}/addresses/{addressId} [patch]
func (ctrl *AddressesHandler) UpdateAddress(ctx http.Context) http.Response {
	addressID, err := requests.RouteUUID(ctx, "addressId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid address id")
	}

	var req requests.UpdateAddressRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
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
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "no fields to update")
	}

	addr, err := ctrl.walletService().UpdateAddress(ctx.Context(), addressID, fields)
	if err != nil {
		return AddressUpdateError(ctx, err)
	}

	return ctx.Response().Success().Json(addressresource.AddressPtr(addr, walletresource.WalletPtr))
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
func (ctrl *AddressesHandler) ListWalletAddresses(ctx http.Context) http.Response {
	walletID, err := requests.RouteUUID(ctx, "walletId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid wallet id")
	}
	limit, offset := pagination.ParseParams(ctx, 20)
	addrs, total, err := ctrl.addresses.PaginateByWalletID(ctx.Context(), walletID, limit, offset)
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to fetch addresses")
	}
	return ctx.Response().Success().Json(pagination.Response(addressresource.AddressesFrom(addrs, walletresource.WalletPtr), total, limit, offset))
}
