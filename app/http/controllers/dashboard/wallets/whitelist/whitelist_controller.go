package whitelist

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	whitelistrequests "github.com/macrowallets/waas/app/http/requests/dashboard/wallets/whitelist"
	"github.com/macrowallets/waas/app/http/resources/dashboard/wallets/whitelist"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// WhitelistController serves the dashboard wallet whitelist routes.
type WhitelistController struct {
	entries *walletrecords.Whitelist
}

// NewWhitelistController wires the controller with the whitelist records.
func NewWhitelistController(entries *walletrecords.Whitelist) *WhitelistController {
	if entries == nil {
		panic("dashboard whitelist controller: whitelist service is required")
	}
	return &WhitelistController{entries: entries}
}

// Index godoc
//
//	@Summary		List whitelist entries for a wallet
//	@Description	Returns all address whitelist entries. Requires wallet or account membership.
//	@Tags			Whitelist
//	@Security		BearerAuth
//	@Produce		json
//	@Param			walletId	path		string	true	"Wallet UUID"
//	@Success		200			{object}	WhitelistEntryListResponse
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/wallets/{walletId}/whitelist [get]
func (c *WhitelistController) Index(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)
	limit, offset := pagination.ParseParams(ctx, 20)

	entries, total, err := c.entries.PaginateByWalletID(ctx.Context(), wallet.ID, limit, offset)
	if err != nil {
		return mapError(ctx, err, "fetch whitelist entries")
	}

	return ctx.Response().Success().Json(pagination.Response(whitelist.WhitelistEntriesFrom(entries), total, limit, offset))
}

// Store godoc
//
//	@Summary		Add an address to the wallet whitelist
//	@Description	Creates a new whitelist entry. Requires wallet or account owner/admin.
//	@Tags			Whitelist
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			walletId	path		string						true	"Wallet UUID"
//	@Param			request		body		AddWhitelistEntrySwagger	true	"Entry payload"
//	@Success		201			{object}	whitelist.WhitelistEntry
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Router			/wallets/{walletId}/whitelist [post]
func (c *WhitelistController) Store(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	var req whitelistrequests.StoreRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	entry, err := c.entries.Add(ctx.Context(), wallet.ID, req.Address, req.Label)
	if err != nil {
		return mapError(ctx, err, "add whitelist entry")
	}

	return ctx.Response().Status(http.StatusCreated).Json(whitelist.WhitelistEntryFrom(*entry))
}

// Destroy godoc
//
//	@Summary		Remove an address from the wallet whitelist
//	@Description	Deletes a whitelist entry by ID. Requires wallet or account owner/admin.
//	@Tags			Whitelist
//	@Security		BearerAuth
//	@Produce		json
//	@Param			walletId	path	string	true	"Wallet UUID"
//	@Param			entryId		path	string	true	"Whitelist entry UUID"
//	@Success		204			"No content"
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/wallets/{walletId}/whitelist/{entryId} [delete]
func (c *WhitelistController) Destroy(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	entryID, err := requests.RouteUUID(ctx, "entryId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid entry id")
	}

	if err := c.entries.Remove(ctx.Context(), wallet.ID, entryID); err != nil {
		return mapError(ctx, err, "delete whitelist entry")
	}

	return ctx.Response().NoContent()
}
