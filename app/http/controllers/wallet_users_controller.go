package controllers

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/models"
)

// ListWalletUsers godoc
// @Summary      List wallet users
// @Description  Returns all active members of a wallet
// @Tags         Wallet Users
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path  string  true  "Wallet UUID"
// @Success      200  {object}  WalletUserListResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /wallets/{walletId}/users [get]
func ListWalletUsers(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)

	members, err := container.Get().WalletUserRepo.FindByWalletID(wallet.ID)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch wallet users"})
	}
	return ctx.Response().Json(http.StatusOK, http.Json{"data": members})
}

// AddWalletUser godoc
// @Summary      Add a user to a wallet
// @Description  Adds a user to the wallet with the specified role. Requires wallet or account owner/admin.
// @Tags         Wallet Users
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        walletId  path      string              true  "Wallet UUID"
// @Param        request   body      AddWalletUserSwagger  true  "User and role payload"
// @Success      201  {object}  models.WalletUser
// @Failure      400  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Router       /wallets/{walletId}/users [post]
func AddWalletUser(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)
	if resp := authorize(ctx, "wallet.add-user", map[string]any{"wallet_id": wallet.ID}); resp != nil {
		return resp
	}

	var req requests.AddWalletUserRequest
	if resp := validateRequest(ctx, &req); resp != nil {
		return resp
	}
	targetID, _ := uuid.Parse(req.UserID)

	existing, existErr := container.Get().WalletUserRepo.FindByWalletAndUserIncludeDeleted(wallet.ID, targetID)
	if existErr != nil {
		facades.Log().WithContext(ctx).Errorf("wallet-users: lookup existing: %v", existErr)
	}
	if existing != nil && existing.DeletedAt != nil {
		if err := container.Get().WalletUserRepo.UpdateField(existing.ID, "deleted_at", nil); err != nil {
			return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to restore wallet user"})
		}
		if req.Roles != "" {
			if err := container.Get().WalletUserRepo.UpdateField(existing.ID, "roles", req.Roles); err != nil {
				facades.Log().WithContext(ctx).Errorf("wallet-users: update roles: %v", err)
			}
		}
		return ctx.Response().Json(http.StatusCreated, existing)
	}

	wu := &models.WalletUser{
		ID:       uuid.New(),
		WalletID: wallet.ID,
		UserID:   targetID,
		Roles:    req.Roles,
		Status:   "active",
	}
	if err := container.Get().WalletUserRepo.Create(wu); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to add wallet user"})
	}
	return ctx.Response().Json(http.StatusCreated, wu)
}

// RemoveWalletUser godoc
// @Summary      Remove a user from a wallet
// @Description  Soft-deletes the wallet_user membership. Requires wallet or account owner/admin.
// @Tags         Wallet Users
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path  string  true  "Wallet UUID"
// @Param        userId    path  string  true  "User UUID to remove"
// @Success      204  "No content"
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /wallets/{walletId}/users/{userId} [delete]
func RemoveWalletUser(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)
	if resp := authorize(ctx, "wallet.remove-user", map[string]any{"wallet_id": wallet.ID}); resp != nil {
		return resp
	}

	targetIDStr := ctx.Request().Route("userId")
	targetID, err := uuid.Parse(targetIDStr)
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid user id"})
	}

	if err := container.Get().WalletUserRepo.SoftDelete(wallet.ID, targetID); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to remove wallet user"})
	}
	return ctx.Response().NoContent()
}

// ---- Request/Response types ----

type AddWalletUserSwagger struct {
	UserID string `json:"user_id" example:"00000000-0000-0000-0000-000000000001"`
	Roles  string `json:"roles" example:"viewer"`
}

type WalletUserListResponse struct {
	Data []models.WalletUser `json:"data"`
}
