package controllers

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
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
		if err := container.Get().UserRepo.UpdateFullName(user.ID, req.FullName); err != nil {
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

	if err := container.Get().UserRepo.UpdatePasswordHash(user.ID, hash); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to update password"})
	}

	return ctx.Response().Json(http.StatusOK, http.Json{"message": "password updated successfully"})
}

// ListMyAccounts godoc
// @Summary      List accounts for current user
// @Description  Returns a paginated list of accounts the authenticated user is a member of
// @Tags         User
// @Security     BearerAuth
// @Produce      json
// @Param        limit   query   int  false  "Max results (default 20)"  example(20)
// @Param        offset  query   int  false  "Pagination offset"         example(0)
// @Success      200  {object}  AccountListResponse
// @Failure      401  {object}  ErrorResponse
// @Router       /users/me/accounts [get]
func ListMyAccounts(ctx http.Context) http.Response {
	userID, ok := ctx.Value("user_id").(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
	}

	limit, offset := pagination.ParseParams(ctx, 20)

	memberships, total, err := container.Get().AccountUserRepo.PaginateByUserID(userID, limit, offset)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch accounts"})
	}

	accountIDs := make([]uuid.UUID, 0, len(memberships))
	for _, m := range memberships {
		accountIDs = append(accountIDs, m.AccountID)
	}

	accounts, err := container.Get().AccountRepo.FindByIDs(accountIDs)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch accounts"})
	}

	return ctx.Response().Json(http.StatusOK, pagination.Response(accounts, total, limit, offset))
}

// UpdateDefaultAccount godoc
// @Summary      Set default account
// @Description  Updates the authenticated user's default account
// @Tags         User
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        request  body      UpdateDefaultAccountRequest  true  "Default account payload"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  ErrorResponse
// @Failure      403      {object}  ErrorResponse
// @Router       /users/me/default-account [patch]
func UpdateDefaultAccount(ctx http.Context) http.Response {
	userID, ok := ctx.Value("user_id").(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
	}

	var req UpdateDefaultAccountRequest
	if err := ctx.Request().Bind(&req); err != nil || req.AccountID == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "account_id is required"})
	}

	accountID, err := uuid.Parse(req.AccountID)
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid account_id"})
	}

	au, err := container.Get().AccountUserRepo.FindByAccountAndUser(accountID, userID)
	if err != nil || au == nil {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "not a member of this account"})
	}

	userPtr, _ := container.Get().UserRepo.FindByID(userID)
	if userPtr == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "user not found"})
	}
	userPtr.DefaultAccountID = &accountID
	if err := container.Get().UserRepo.UpdateDefaultAccountID(userPtr.ID, &accountID); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to update default account"})
	}

	account, _ := container.Get().AccountRepo.FindByID(accountID)
	return ctx.Response().Json(http.StatusOK, http.Json{"account": account})
}

// ---- Request/Response types ----

type UpdateMeRequest struct {
	FullName string `json:"full_name" example:"Alice Smith"`
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type UpdateDefaultAccountRequest struct {
	AccountID string `json:"account_id" example:"550e8400-e29b-41d4-a716-446655440000"`
}

type AccountListResponse struct {
	Data []models.Account `json:"data"`
}
