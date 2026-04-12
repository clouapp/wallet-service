package controllers

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/models"
)

// ListWhitelistEntries godoc
// @Summary      List whitelist entries for a wallet
// @Description  Returns all address whitelist entries. Requires wallet or account membership.
// @Tags         Whitelist
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path  string  true  "Wallet UUID"
// @Success      200  {object}  WhitelistEntryListResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /wallets/{walletId}/whitelist [get]
func ListWhitelistEntries(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)

	limit, offset := pagination.ParseParams(ctx, 20)
	entries, total, err := container.Get().WhitelistEntryRepo.PaginateByWalletID(wallet.ID, limit, offset)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch whitelist entries"})
	}
	return ctx.Response().Json(http.StatusOK, pagination.Response(entries, total, limit, offset))
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
// @Success      201  {object}  models.WhitelistEntry
// @Failure      400  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Router       /wallets/{walletId}/whitelist [post]
func AddWhitelistEntry(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)
	if resp := authorize(ctx, "wallet.whitelist", map[string]any{"wallet_id": wallet.ID}); resp != nil {
		return resp
	}

	var req requests.AddWhitelistEntryRequest
	if resp := validateRequest(ctx, &req); resp != nil {
		return resp
	}

	entry := &models.WhitelistEntry{
		ID:       uuid.New(),
		WalletID: wallet.ID,
		Address:  req.Address,
		Label:    req.Label,
	}
	if err := container.Get().WhitelistEntryRepo.Create(entry); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to add whitelist entry"})
	}
	return ctx.Response().Json(http.StatusCreated, entry)
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
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /wallets/{walletId}/whitelist/{entryId} [delete]
func DeleteWhitelistEntry(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)
	if resp := authorize(ctx, "wallet.whitelist", map[string]any{"wallet_id": wallet.ID}); resp != nil {
		return resp
	}

	entryIDStr := ctx.Request().Route("entryId")
	entryID, err := uuid.Parse(entryIDStr)
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid entry id"})
	}

	entry, err := container.Get().WhitelistEntryRepo.FindByIDAndWallet(entryID, wallet.ID)
	if err != nil || entry == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "whitelist entry not found"})
	}

	if err := container.Get().WhitelistEntryRepo.Delete(entry); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to delete whitelist entry"})
	}
	return ctx.Response().NoContent()
}

// ---- Request/Response types ----

type AddWhitelistEntrySwagger struct {
	Address string `json:"address" example:"bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq"`
	Label   string `json:"label,omitempty" example:"Cold Storage"`
}

type WhitelistEntryListResponse struct {
	Data []models.WhitelistEntry `json:"data"`
}
