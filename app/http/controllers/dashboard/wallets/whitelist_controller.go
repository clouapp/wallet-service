package wallets

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/resources/dashboard/wallets/whitelist"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// WhitelistController serves the dashboard wallet whitelist routes.
type WhitelistController struct {
	entries     *walletrecords.Whitelist
	memberships *walletrecords.Memberships
}

// WhitelistControllerDeps is everything the dashboard whitelist controller needs.
// Every field is required.
type WhitelistControllerDeps struct {
	Entries     *walletrecords.Whitelist
	Memberships *walletrecords.Memberships
}

// NewWhitelistController wires the dashboard whitelist handlers from WhitelistControllerDeps.
func NewWhitelistController(deps WhitelistControllerDeps) *WhitelistController {
	if deps.Entries == nil {
		panic("dashboard whitelist controller: whitelist service is required")
	}
	if deps.Memberships == nil {
		panic("dashboard whitelist controller: wallet memberships are required")
	}
	return &WhitelistController{
		entries:     deps.Entries,
		memberships: deps.Memberships,
	}
}

// ListWhitelistEntries godoc
// @Summary      List whitelist entries for a wallet
// @Description  Returns all address whitelist entries. Requires wallet or account membership.
// @Tags         Whitelist
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path  string  true  "Wallet UUID"
// @Success      200  {object}  WhitelistEntryListResponse
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /wallets/{walletId}/whitelist [get]
func (ctrl *WhitelistController) ListWhitelistEntries(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	limit, offset := pagination.ParseParams(ctx, 20)
	entries, total, err := ctrl.entries.PaginateByWalletID(ctx.Context(), wallet.ID, limit, offset)
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to fetch whitelist entries")
	}
	return ctx.Response().Success().Json(pagination.Response(whitelist.WhitelistEntriesFrom(entries), total, limit, offset))
}

// AddWhitelistEntry godoc
// @Summary      Add an address to the wallet whitelist
// @Description  Creates a new whitelist entry. Requires wallet or account owner/admin.
// @Tags         Whitelist
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        walletId  path      string                  true  "Wallet UUID"
// @Param        request   body      AddWhitelistEntrySwagger  true  "Entry payload"
// @Success      201  {object}  whitelist.WhitelistEntry
// @Failure      400  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Router       /wallets/{walletId}/whitelist [post]
func (ctrl *WhitelistController) AddWhitelistEntry(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	var req requests.AddWhitelistEntryRequest
	if resp := requests.Validate(ctx, &req); resp != nil {
		return resp
	}

	entry := &models.WhitelistEntry{
		ID:       uuid.New(),
		WalletID: wallet.ID,
		Address:  req.Address,
		Label:    req.Label,
	}
	if err := ctrl.entries.Create(ctx.Context(), entry); err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to add whitelist entry")
	}
	return ctx.Response().Status(http.StatusCreated).Json(whitelist.WhitelistEntryFrom(*entry))
}

// DeleteWhitelistEntry godoc
// @Summary      Remove an address from the wallet whitelist
// @Description  Deletes a whitelist entry by ID. Requires wallet or account owner/admin.
// @Tags         Whitelist
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path  string  true  "Wallet UUID"
// @Param        entryId   path  string  true  "Whitelist entry UUID"
// @Success      204  "No content"
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /wallets/{walletId}/whitelist/{entryId} [delete]
func (ctrl *WhitelistController) DeleteWhitelistEntry(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	entryID, err := requests.RouteUUID(ctx, "entryId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid entry id")
	}

	entry, err := ctrl.entries.FindByIDAndWallet(ctx.Context(), entryID, wallet.ID)
	if err != nil || entry == nil {
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "whitelist entry not found")
	}

	if err := ctrl.entries.Delete(ctx.Context(), entry); err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to delete whitelist entry")
	}
	return ctx.Response().NoContent()
}

// ---- Request/Response types ----

type AddWhitelistEntrySwagger struct {
	Address string `json:"address" example:"bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq"`
	Label   string `json:"label,omitempty" example:"Cold Storage"`
}

type WhitelistEntryListResponse struct {
	Data []whitelist.WhitelistEntry `json:"data"`
}
