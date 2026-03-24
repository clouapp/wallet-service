package controllers

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macromarkets/vault/app/models"
	authsvc "github.com/macromarkets/vault/app/services/auth"
)

var userAuthService = authsvc.NewService()

// GetMe godoc
// @Summary      Get current user profile
// @Description  Returns the authenticated user's profile
// @Tags         User
// @Security     BearerAuth
// @Produce      json
// @Success      200  {object}  models.User
// @Failure      401  {object}  ErrorResponse
// @Router       /users/me [get]
func GetMe(ctx http.Context) http.Response {
	user, ok := ctx.Value("user").(*models.User)
	if !ok || user == nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
	}
	return ctx.Response().Json(http.StatusOK, user)
}

// UpdateMe godoc
// @Summary      Update current user profile
// @Description  Updates the authenticated user's full name
// @Tags         User
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        request  body      UpdateMeRequest  true  "Update payload"
// @Success      200      {object}  models.User
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Router       /users/me [patch]
func UpdateMe(ctx http.Context) http.Response {
	user, ok := ctx.Value("user").(*models.User)
	if !ok || user == nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
	}

	var req UpdateMeRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}

	if req.FullName != "" {
		if _, err := facades.Orm().Query().Model(user).
			Where("id = ?", user.ID).
			Update("full_name", req.FullName); err != nil {
			return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to update profile"})
		}
		user.FullName = req.FullName
	}

	return ctx.Response().Json(http.StatusOK, user)
}

// ChangePassword godoc
// @Summary      Change password
// @Description  Validates the current password and updates it
// @Tags         User
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        request  body      ChangePasswordRequest  true  "Password change payload"
// @Success      200      {object}  map[string]string
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Router       /users/me/password [post]
func ChangePassword(ctx http.Context) http.Response {
	user, ok := ctx.Value("user").(*models.User)
	if !ok || user == nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
	}

	var req ChangePasswordRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "current_password and new_password are required"})
	}

	if !userAuthService.CheckPassword(req.CurrentPassword, user.PasswordHash) {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "current password is incorrect"})
	}

	hash, err := userAuthService.HashPassword(req.NewPassword)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to hash password"})
	}

	if _, err := facades.Orm().Query().Model(user).
		Where("id = ?", user.ID).
		Update("password_hash", hash); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to update password"})
	}

	return ctx.Response().Json(http.StatusOK, http.Json{"message": "password updated successfully"})
}

// ListMyAccounts godoc
// @Summary      List accounts for current user
// @Description  Returns all accounts the authenticated user is a member of
// @Tags         User
// @Security     BearerAuth
// @Produce      json
// @Success      200  {object}  AccountListResponse
// @Failure      401  {object}  ErrorResponse
// @Router       /users/me/accounts [get]
func ListMyAccounts(ctx http.Context) http.Response {
	userID, ok := ctx.Value("user_id").(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
	}

	var memberships []models.AccountUser
	if err := facades.Orm().Query().
		Where("user_id = ? AND deleted_at IS NULL", userID).
		Find(&memberships); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch accounts"})
	}

	accountIDs := make([]uuid.UUID, 0, len(memberships))
	for _, m := range memberships {
		accountIDs = append(accountIDs, m.AccountID)
	}

	var accounts []models.Account
	if len(accountIDs) > 0 {
		if err := facades.Orm().Query().
			Where("id IN ?", accountIDs).
			Find(&accounts); err != nil {
			return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch accounts"})
		}
	}

	return ctx.Response().Json(http.StatusOK, http.Json{"data": accounts})
}

// ---- Request/Response types ----

type UpdateMeRequest struct {
	FullName string `json:"full_name" example:"Alice Smith"`
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type AccountListResponse struct {
	Data []models.Account `json:"data"`
}
