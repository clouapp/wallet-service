package addresses

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	addressresource "github.com/macrowallets/waas/app/http/resources/addresses"
	"github.com/macrowallets/waas/app/http/responses"
	deposit "github.com/macrowallets/waas/app/services/deposit"
	wallet "github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

func validateRequest(ctx http.Context, req http.FormRequest) http.Response {
	return controllers.ValidateRequest(ctx, req)
}

// AddressesController serves the dashboard address routes.
type AddressesController struct {
	addresses     *walletrecords.Addresses
	walletService func() *wallet.Service
	deposits      *deposit.Service
}

func NewAddressesController(
	addresses *walletrecords.Addresses,
	walletService func() *wallet.Service,
	deposits *deposit.Service,
) *AddressesController {
	if addresses == nil {
		panic("dashboard addresses controller: addresses service is required")
	}
	if walletService == nil || walletService() == nil {
		panic("dashboard addresses controller: wallet service is required")
	}
	if deposits == nil {
		panic("dashboard addresses controller: deposit service is required")
	}
	return &AddressesController{
		addresses:     addresses,
		walletService: walletService,
		deposits:      deposits,
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
func (ctrl *AddressesController) GenerateAddress(ctx http.Context) http.Response {
	walletID, err := requests.RouteUUID(ctx, "walletId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{
			"error": "invalid wallet id",
		})
	}

	var req requests.GenerateAddressRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	addr, err := ctrl.walletService().GenerateAddress(ctx.Context(), walletID, req.ExternalUserID, req.Label, req.Metadata, req.Passphrase)
	if err != nil {
		return responses.Send(ctx, http.StatusUnprocessableEntity, http.Json{
			"error": err.Error(),
		})
	}

	// Refresh Redis address cache for the chain
	if w, err := ctrl.walletService().GetWallet(ctx.Context(), walletID); err == nil {
		ctrl.deposits.RefreshAddressCache(ctx.Context(), w.Chain)
	}

	return responses.Send(ctx, http.StatusCreated, addressresource.AddressPtr(addr, controllers.WalletBodyViewPtr))
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
func (ctrl *AddressesController) UpdateAddress(ctx http.Context) http.Response {
	addressID, err := requests.RouteUUID(ctx, "addressId")
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

	addr, err := ctrl.walletService().UpdateAddress(ctx.Context(), addressID, fields)
	if err != nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{
			"error": err.Error(),
		})
	}

	return ctx.Response().Success().Json(addressresource.AddressPtr(addr, controllers.WalletBodyViewPtr))
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
func (ctrl *AddressesController) ListWalletAddresses(ctx http.Context) http.Response {
	walletID, err := requests.RouteUUID(ctx, "walletId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{
			"error": "invalid wallet id",
		})
	}
	limit, offset := pagination.ParseParams(ctx, 20)
	addrs, total, err := ctrl.addresses.PaginateByWalletID(ctx.Context(), walletID, limit, offset)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{
			"error": "failed to fetch addresses",
		})
	}
	return responses.Send(ctx, http.StatusOK, pagination.Response(addressresource.AddressesFrom(addrs, controllers.WalletBodyViewPtr), total, limit, offset))
}
