package addresses

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	addressresource "github.com/macrowallets/waas/app/http/resources/addresses"
	walletresource "github.com/macrowallets/waas/app/http/resources/dashboard/wallets"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/walletops"
)

// AddressController serves the external address routes: the handlers shared
// with the dashboard (Store, Update, Index) plus the lookup routes only this
// surface exposes.
type AddressController struct {
	*controllers.AddressesHandler
	ops *walletops.Service
}

// NewAddressController wires the external address handlers with the wallet operations.
func NewAddressController(ops *walletops.Service) *AddressController {
	return &AddressController{
		AddressesHandler: controllers.NewAddressesHandler("external", ops),
		ops:              ops,
	}
}

// Show godoc
//
//	@Summary		Look up an address
//	@Description	Finds a deposit address record by its on-chain address string. Optionally filter by chain.
//	@Tags			Addresses
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		SignatureAuth
//	@Param			address	path		string	true	"On-chain address"	example("0xABCDEF1234567890")
//	@Param			chain	query		string	false	"Chain ID filter"	example("eth")
//	@Success		200		{object}	addressresource.Address
//	@Failure		400		{object}	responses.ErrorBody	"Missing chain parameter"
//	@Failure		404		{object}	responses.ErrorBody	"Address not found"
//	@Failure		429		{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/v1/addresses/{address} [get]
func (c *AddressController) Show(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "unauthorized")
	}
	address := ctx.Request().Route("address")
	chainID := ctx.Request().Query("chain")

	found, err := c.ops.LookupAddress(ctx.Context(), accountID, address, chainID)
	if err != nil {
		return mapError(ctx, err, "lookup_address")
	}

	return ctx.Response().Success().Json(addressresource.AddressPtr(found, walletresource.WalletPtr))
}

// ByUser godoc
//
//	@Summary		List addresses for a user
//	@Description	Returns all deposit addresses assigned to a specific external user ID across all chains
//	@Tags			Addresses
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		SignatureAuth
//	@Param			external_id	path		string	true	"External user identifier"	example("user_123")
//	@Success		200			{object}	controllers.AddressListResponse
//	@Failure		500			{object}	responses.ErrorBody
//	@Failure		429			{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/v1/users/{external_id}/addresses [get]
func (c *AddressController) ByUser(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "unauthorized")
	}
	externalUserID := ctx.Request().Route("external_id")

	addresses, err := c.ops.UserAddresses(ctx.Context(), accountID, externalUserID)
	if err != nil {
		return mapError(ctx, err, "list_user_addresses")
	}

	// An empty slice is the honest response for both "no such external_id" and
	// "external_id exists under another account". Do not distinguish.
	return ctx.Response().Success().Json(http.Json{"data": addressresource.AddressesFrom(addresses, walletresource.WalletPtr)})
}
