package addresses

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	addressresource "github.com/macrowallets/waas/app/http/resources/addresses"
	walletresource "github.com/macrowallets/waas/app/http/resources/dashboard/wallets"
	"github.com/macrowallets/waas/app/http/responses"
	chain "github.com/macrowallets/waas/app/services/chain"
	deposit "github.com/macrowallets/waas/app/services/deposit"
	wallet "github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// AddressesController serves the external address routes: the handlers shared
// with the dashboard plus the lookup routes only this surface exposes.
type AddressesController struct {
	*controllers.AddressesHandler
	walletService func() *wallet.Service
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
	shared := controllers.NewAddressesHandler("external", controllers.AddressesHandlerDeps{
		Addresses:     deps.Addresses,
		WalletService: deps.WalletService,
		Deposits:      deps.Deposits,
	})
	if deps.Registry == nil {
		panic("external addresses controller: chain registry is required")
	}
	return &AddressesController{
		AddressesHandler: shared,
		walletService:    deps.WalletService,
		registry:         deps.Registry,
	}
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
// @Failure      400      {object}  responses.ErrorBody  "Missing chain parameter"
// @Failure      404      {object}  responses.ErrorBody  "Address not found"
// @Failure      429  {object}  responses.ErrorBody  "Rate limit exceeded (too_many_requests, Retry-After header)"
// @Router       /v1/addresses/{address} [get]
func (ctrl *AddressesController) LookupAddress(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "unauthorized")
	}

	address := ctx.Request().Route("address")
	chainFilter := ctx.Request().Query("chain")

	if chainFilter != "" {
		addr, err := ctrl.walletService().LookupAddressForAccount(ctx.Context(), chainFilter, address, accountID)
		if err != nil || addr == nil {
			return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "address not found")
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
	return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "address not found")
}

// ListUserAddresses godoc
// @Summary      List addresses for a user
// @Description  Returns all deposit addresses assigned to a specific external user ID across all chains
// @Tags         Addresses
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        external_id  path      string  true  "External user identifier"  example("user_123")
// @Success      200          {object}  controllers.AddressListResponse
// @Failure      500          {object}  responses.ErrorBody
// @Failure      429  {object}  responses.ErrorBody  "Rate limit exceeded (too_many_requests, Retry-After header)"
// @Router       /v1/users/{external_id}/addresses [get]
func (ctrl *AddressesController) ListUserAddresses(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "unauthorized")
	}

	addrs, err := ctrl.walletService().ListUserAddressesForAccount(
		ctx.Context(),
		ctx.Request().Route("external_id"),
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
