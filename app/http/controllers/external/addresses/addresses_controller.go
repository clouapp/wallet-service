package addresses

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	addressresource "github.com/macrowallets/waas/app/http/resources/addresses"
	walletresource "github.com/macrowallets/waas/app/http/resources/dashboard/wallets"
	"github.com/macrowallets/waas/app/http/responses"
	chain "github.com/macrowallets/waas/app/services/chain"
	deposit "github.com/macrowallets/waas/app/services/deposit"
	wallet "github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

func validateRequest(ctx http.Context, req http.FormRequest) http.Response {
	return controllers.ValidateRequest(ctx, req)
}

// AddressesController serves the external address routes.
type AddressesController struct {
	addresses     *walletrecords.Addresses
	walletService func() *wallet.Service
	deposits      *deposit.Service
	registry      *chain.Registry
}

// AddressesControllerDeps is everything the external addresses controller needs.
// Every field is required.
type AddressesControllerDeps struct {
	Addresses     *walletrecords.Addresses
	WalletService func() *wallet.Service
	Deposits      *deposit.Service
	Registry      *chain.Registry
}

// NewAddressesController wires the external address handlers from AddressesControllerDeps.
func NewAddressesController(deps AddressesControllerDeps) *AddressesController {
	if deps.Addresses == nil {
		panic("external addresses controller: addresses service is required")
	}
	if deps.WalletService == nil || deps.WalletService() == nil {
		panic("external addresses controller: wallet service is required")
	}
	if deps.Deposits == nil {
		panic("external addresses controller: deposit service is required")
	}
	if deps.Registry == nil {
		panic("external addresses controller: chain registry is required")
	}
	return &AddressesController{
		addresses:     deps.Addresses,
		walletService: deps.WalletService,
		deposits:      deps.Deposits,
		registry:      deps.Registry,
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
	defer controllers.DiscardPassphrase(&req.Passphrase)
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	addr, err := ctrl.walletService().GenerateAddress(ctx.Context(), walletID, req.ExternalUserID, req.Label, req.Metadata, req.Passphrase)
	if err != nil {
		return controllers.AddressGenerationError(ctx, err)
	}

	// Refresh Redis address cache for the chain
	if w, err := ctrl.walletService().GetWallet(ctx.Context(), walletID); err == nil {
		ctrl.deposits.RefreshAddressCache(ctx.Context(), w.Chain)
	}

	return responses.Send(ctx, http.StatusCreated, addressresource.AddressPtr(addr, walletresource.WalletPtr))
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
		return controllers.AddressUpdateError(ctx, err)
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
	return responses.Send(ctx, http.StatusOK, pagination.Response(addressresource.AddressesFrom(addrs, walletresource.WalletPtr), total, limit, offset))
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
// @Success      200      {object}  addressresource.Address
// @Failure      400      {object}  ErrorResponse  "Missing chain parameter"
// @Failure      404      {object}  ErrorResponse  "Address not found"
// @Router       /v1/addresses/{address} [get]
func (ctrl *AddressesController) LookupAddress(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{
			"error": "unauthorized",
		})
	}

	var path requests.LookupAddressRequest
	path.Load(ctx)
	address := path.Address
	chainFilter := path.Chain

	if chainFilter != "" {
		addr, err := ctrl.walletService().LookupAddressForAccount(ctx.Context(), chainFilter, address, accountID)
		if err != nil || addr == nil {
			return responses.Send(ctx, http.StatusNotFound, http.Json{
				"error": "address not found",
			})
		}
		return ctx.Response().Success().Json(addressresource.AddressPtr(addr, walletresource.WalletPtr))
	}

	// Try all chains — still scoped to the caller's account so a hit on any
	// chain that belongs to a different account does not leak.
	for _, id := range ctrl.registry.ChainIDs() {
		addr, err := ctrl.walletService().LookupAddressForAccount(ctx.Context(), id, address, accountID)
		if err == nil && addr != nil {
			return ctx.Response().Success().Json(addressresource.AddressPtr(addr, walletresource.WalletPtr))
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
func (ctrl *AddressesController) ListUserAddresses(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{
			"error": "unauthorized",
		})
	}

	var path requests.ExternalIDRequest
	path.Load(ctx)
	addrs, err := ctrl.walletService().ListUserAddressesForAccount(
		ctx.Context(),
		path.ExternalID,
		accountID,
	)
	if err != nil {
		return controllers.MapInternalError(ctx, err, "list_user_addresses")
	}
	// An empty slice is the honest response for both "no such external_id"
	// and "external_id exists under another account". Do not distinguish.
	return ctx.Response().Success().Json(http.Json{
		"data": addressresource.AddressesFrom(addrs, walletresource.WalletPtr),
	})
}
