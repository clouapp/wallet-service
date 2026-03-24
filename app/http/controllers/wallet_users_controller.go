package controllers

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macromarkets/vault/app/models"
)

// walletFromParam resolves the {walletId} route parameter and verifies
// the caller is a member of the wallet's account or the wallet itself.
// Returns (wallet, callerAccountRole, callerWalletRole, errResponse).
func walletFromParam(ctx http.Context) (*models.Wallet, string, string, http.Response) {
	rawID := ctx.Request().Input("walletId")
	walletID, err := uuid.Parse(rawID)
	if err != nil {
		return nil, "", "", ctx.Response().Json(http.StatusNotFound, http.Json{"error": "invalid wallet id"})
	}

	var wallet models.Wallet
	if err := facades.Orm().Query().Where("id = ?", walletID).First(&wallet); err != nil {
		return nil, "", "", ctx.Response().Json(http.StatusNotFound, http.Json{"error": "wallet not found"})
	}

	userID, _ := ctx.Value("user_id").(uuid.UUID)

	// Account-level role
	accRole := ""
	if wallet.AccountID != nil {
		var au models.AccountUser
		if err := facades.Orm().Query().
			Where("account_id = ? AND user_id = ? AND deleted_at IS NULL", *wallet.AccountID, userID).
			First(&au); err == nil {
			accRole = au.Role
		}
	}

	// Wallet-level role
	walletRole := ""
	var wu models.WalletUser
	if err := facades.Orm().Query().
		Where("wallet_id = ? AND user_id = ? AND deleted_at IS NULL", walletID, userID).
		First(&wu); err == nil {
		walletRole = wu.Roles
	}

	if accRole == "" && walletRole == "" {
		return nil, "", "", ctx.Response().Json(http.StatusForbidden, http.Json{"error": "not a member of this wallet or its account"})
	}

	return &wallet, accRole, walletRole, nil
}

// isWalletAdmin returns true if the caller has admin or owner privileges on the wallet.
func isWalletAdmin(accRole, walletRole string) bool {
	return accRole == "owner" || accRole == "admin" || walletRole == "owner" || walletRole == "admin"
}

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
	wallet, _, _, errResp := walletFromParam(ctx)
	if errResp != nil {
		return errResp
	}

	var members []models.WalletUser
	if err := facades.Orm().Query().
		Where("wallet_id = ? AND deleted_at IS NULL", wallet.ID).
		Find(&members); err != nil {
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
// @Param        request   body      AddWalletUserRequest  true  "User and role payload"
// @Success      201  {object}  models.WalletUser
// @Failure      400  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Router       /wallets/{walletId}/users [post]
func AddWalletUser(ctx http.Context) http.Response {
	wallet, accRole, walletRole, errResp := walletFromParam(ctx)
	if errResp != nil {
		return errResp
	}
	if !isWalletAdmin(accRole, walletRole) {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "only wallet/account owners and admins may add users"})
	}

	var req AddWalletUserRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
	if req.UserID == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "user_id is required"})
	}
	targetID, err := uuid.Parse(req.UserID)
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid user_id"})
	}

	// Check for existing (possibly soft-deleted) entry
	var existing models.WalletUser
	lookupErr := facades.Orm().Query().
		Where("wallet_id = ? AND user_id = ?", wallet.ID, targetID).
		First(&existing)
	if lookupErr == nil && existing.DeletedAt != nil {
		// Re-activate
		_, _ = facades.Orm().Query().Model(&existing).Where("id = ?", existing.ID).Update("deleted_at", nil)
		if req.Roles != "" {
			_, _ = facades.Orm().Query().Model(&existing).Where("id = ?", existing.ID).Update("roles", req.Roles)
		}
		return ctx.Response().Json(http.StatusCreated, &existing)
	}

	wu := &models.WalletUser{
		ID:       uuid.New(),
		WalletID: wallet.ID,
		UserID:   targetID,
		Roles:    req.Roles,
		Status:   "active",
	}
	if err := facades.Orm().Query().Create(wu); err != nil {
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
	wallet, accRole, walletRole, errResp := walletFromParam(ctx)
	if errResp != nil {
		return errResp
	}
	if !isWalletAdmin(accRole, walletRole) {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "only wallet/account owners and admins may remove users"})
	}

	targetIDStr := ctx.Request().Route("userId")
	targetID, err := uuid.Parse(targetIDStr)
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid user id"})
	}

	now := time.Now()
	if _, err := facades.Orm().Query().
		Model(&models.WalletUser{}).
		Where("wallet_id = ? AND user_id = ? AND deleted_at IS NULL", wallet.ID, targetID).
		Update("deleted_at", now); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to remove wallet user"})
	}
	return ctx.Response().NoContent()
}

// ---- Request/Response types ----

type AddWalletUserRequest struct {
	UserID string `json:"user_id" example:"00000000-0000-0000-0000-000000000001"`
	Roles  string `json:"roles" example:"viewer"`
}

type WalletUserListResponse struct {
	Data []models.WalletUser `json:"data"`
}
