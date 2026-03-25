package controllers

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
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
	wallet, _, _, errResp := walletFromParam(ctx)
	if errResp != nil {
		return errResp
	}

	entries, err := container.Get().WhitelistEntryRepo.FindByWalletID(wallet.ID)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch whitelist entries"})
	}
	return ctx.Response().Json(http.StatusOK, http.Json{"data": entries})
}

// AddWhitelistEntry godoc
// @Summary      Add an address to the wallet whitelist
// @Description  Creates a new whitelist entry. Requires wallet or account owner/admin.
// @Tags         Whitelist
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        walletId  path      string                  true  "Wallet UUID"
// @Param        request   body      AddWhitelistEntryRequest  true  "Entry payload"
// @Success      201  {object}  models.WhitelistEntry
// @Failure      400  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Router       /wallets/{walletId}/whitelist [post]
func AddWhitelistEntry(ctx http.Context) http.Response {
	wallet, accRole, walletRole, errResp := walletFromParam(ctx)
	if errResp != nil {
		return errResp
	}
	if !isWalletAdmin(accRole, walletRole) {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "only wallet/account owners and admins may manage the whitelist"})
	}

	var req AddWhitelistEntryRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
	if req.Address == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "address is required"})
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
	wallet, accRole, walletRole, errResp := walletFromParam(ctx)
	if errResp != nil {
		return errResp
	}
	if !isWalletAdmin(accRole, walletRole) {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "only wallet/account owners and admins may manage the whitelist"})
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

type AddWhitelistEntryRequest struct {
	Address string `json:"address" example:"bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq"`
	Label   string `json:"label,omitempty" example:"Cold Storage"`
}

type WhitelistEntryListResponse struct {
	Data []models.WhitelistEntry `json:"data"`
}
