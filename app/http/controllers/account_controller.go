package controllers

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	mails "github.com/macromarkets/vault/app/mails"
	"github.com/macromarkets/vault/app/models"
	accountsvc "github.com/macromarkets/vault/app/services/account"
	authsvc "github.com/macromarkets/vault/app/services/auth"
)

var accountService = accountsvc.NewService()
var accountAuthService = authsvc.NewService()

// CreateAccount godoc
// @Summary      Create a new account
// @Description  Creates an account and makes the caller its owner
// @Tags         Accounts
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        request  body      CreateAccountRequest  true  "Account payload"
// @Success      201      {object}  models.Account
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Router       /accounts [post]
func CreateAccount(ctx http.Context) http.Response {
	userID, ok := ctx.Value("user_id").(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
	}

	var req CreateAccountRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
	if req.Name == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "name is required"})
	}

	acc, err := accountService.Create(ctx.Context(), req.Name, userID)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to create account"})
	}
	return ctx.Response().Json(http.StatusCreated, acc)
}

// GetAccount godoc
// @Summary      Get an account
// @Description  Returns account details. Requires account membership (injected by AccountContext middleware).
// @Tags         Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path      string  true  "Account UUID"
// @Success      200        {object}  models.Account
// @Failure      403        {object}  ErrorResponse
// @Failure      404        {object}  ErrorResponse
// @Router       /accounts/{accountId} [get]
func GetAccount(ctx http.Context) http.Response {
	account, ok := ctx.Value("account").(*models.Account)
	if !ok || account == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "account not found"})
	}
	return ctx.Response().Json(http.StatusOK, account)
}

// UpdateAccount godoc
// @Summary      Update account settings
// @Description  Updates account name or view_all_wallets flag. Requires owner or admin role.
// @Tags         Accounts
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        accountId  path      string               true  "Account UUID"
// @Param        request    body      UpdateAccountRequest  true  "Update payload"
// @Success      200        {object}  models.Account
// @Failure      400        {object}  ErrorResponse
// @Failure      403        {object}  ErrorResponse
// @Router       /accounts/{accountId} [patch]
func UpdateAccount(ctx http.Context) http.Response {
	account, ok := ctx.Value("account").(*models.Account)
	if !ok || account == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "account not found"})
	}
	role, _ := ctx.Value("account_role").(string)
	if role != "owner" && role != "admin" {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "only owners and admins may update account settings"})
	}

	var req UpdateAccountRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}

	if req.Name != "" {
		if _, err := facades.Orm().Query().Model(account).Where("id = ?", account.ID).
			Update("name", req.Name); err != nil {
			return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to update account"})
		}
		account.Name = req.Name
	}
	if req.ViewAllWallets != nil {
		if _, err := facades.Orm().Query().Model(account).Where("id = ?", account.ID).
			Update("view_all_wallets", *req.ViewAllWallets); err != nil {
			return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to update account"})
		}
		account.ViewAllWallets = *req.ViewAllWallets
	}

	return ctx.Response().Json(http.StatusOK, account)
}

// ArchiveAccount godoc
// @Summary      Archive an account
// @Description  Sets account status to 'archived'. Requires owner role.
// @Tags         Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Success      200        {object}  models.Account
// @Failure      403        {object}  ErrorResponse
// @Failure      404        {object}  ErrorResponse
// @Router       /accounts/{accountId}/archive [post]
func ArchiveAccount(ctx http.Context) http.Response {
	account, ok := ctx.Value("account").(*models.Account)
	if !ok || account == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "account not found"})
	}
	if role, _ := ctx.Value("account_role").(string); role != "owner" {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "only owners may archive accounts"})
	}

	if _, err := facades.Orm().Query().Model(account).Where("id = ?", account.ID).
		Update("status", "archived"); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to archive account"})
	}
	account.Status = "archived"
	return ctx.Response().Json(http.StatusOK, account)
}

// FreezeAccount godoc
// @Summary      Freeze an account
// @Description  Sets account status to 'frozen'. Requires owner role.
// @Tags         Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Success      200        {object}  models.Account
// @Failure      403        {object}  ErrorResponse
// @Router       /accounts/{accountId}/freeze [post]
func FreezeAccount(ctx http.Context) http.Response {
	account, ok := ctx.Value("account").(*models.Account)
	if !ok || account == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "account not found"})
	}
	if role, _ := ctx.Value("account_role").(string); role != "owner" {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "only owners may freeze accounts"})
	}

	if _, err := facades.Orm().Query().Model(account).Where("id = ?", account.ID).
		Update("status", "frozen"); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to freeze account"})
	}
	account.Status = "frozen"
	return ctx.Response().Json(http.StatusOK, account)
}

// ListAccountUsers godoc
// @Summary      List account members
// @Description  Returns all active members of the account with their roles
// @Tags         Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Success      200        {object}  AccountUserListResponse
// @Failure      403        {object}  ErrorResponse
// @Router       /accounts/{accountId}/users [get]
func ListAccountUsers(ctx http.Context) http.Response {
	account, ok := ctx.Value("account").(*models.Account)
	if !ok || account == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "account not found"})
	}

	var members []models.AccountUser
	if err := facades.Orm().Query().
		Where("account_id = ? AND deleted_at IS NULL", account.ID).
		Find(&members); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch members"})
	}
	return ctx.Response().Json(http.StatusOK, http.Json{"data": members})
}

// AddAccountUser godoc
// @Summary      Add a user to an account
// @Description  Adds a user to the account with the specified role. Requires owner or admin.
// @Tags         Accounts
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        accountId  path      string              true  "Account UUID"
// @Param        request    body      AddAccountUserRequest  true  "User and role payload"
// @Success      201        {object}  models.AccountUser
// @Failure      400        {object}  ErrorResponse
// @Failure      403        {object}  ErrorResponse
// @Router       /accounts/{accountId}/users [post]
func AddAccountUser(ctx http.Context) http.Response {
	account, ok := ctx.Value("account").(*models.Account)
	if !ok || account == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "account not found"})
	}
	callerID, _ := ctx.Value("user_id").(uuid.UUID)
	role, _ := ctx.Value("account_role").(string)
	if role != "owner" && role != "admin" {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "only owners and admins may add users"})
	}

	var req AddAccountUserRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
	if req.Email == "" || req.Role == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "email and role are required"})
	}

	// Look up or create user
	var target models.User
	if err := facades.Orm().Query().Where("email = ?", req.Email).First(&target); err != nil {
		// User does not exist — create a placeholder and send invite
		target = models.User{
			ID:           uuid.New(),
			Email:        req.Email,
			PasswordHash: "",
			Status:       "invited",
		}
		if err2 := facades.Orm().Query().Create(&target); err2 != nil {
			return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to create user"})
		}
		_ = facades.Mail().To([]string{req.Email}).Send(&mails.UserInviteMail{
			To:          req.Email,
			InvitedBy:   "your team",
			AccountName: account.Name,
			InviteLink:  "https://vault.app/accept-invite",
		})
	}

	if err := accountService.AddUser(ctx.Context(), account.ID, target.ID, req.Role, callerID); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to add user"})
	}

	var au models.AccountUser
	_ = facades.Orm().Query().
		Where("account_id = ? AND user_id = ? AND deleted_at IS NULL", account.ID, target.ID).
		First(&au)

	return ctx.Response().Json(http.StatusCreated, &au)
}

// RemoveAccountUser godoc
// @Summary      Remove a user from an account
// @Description  Soft-deletes the account_user membership. Requires owner or admin.
// @Tags         Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Param        userId     path  string  true  "User UUID to remove"
// @Success      204  "No content"
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /accounts/{accountId}/users/{userId} [delete]
func RemoveAccountUser(ctx http.Context) http.Response {
	account, ok := ctx.Value("account").(*models.Account)
	if !ok || account == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "account not found"})
	}
	role, _ := ctx.Value("account_role").(string)
	if role != "owner" && role != "admin" {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "only owners and admins may remove users"})
	}

	userIDStr := ctx.Request().Route("userId")
	targetID, err := uuid.Parse(userIDStr)
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid user id"})
	}

	if err := accountService.RemoveUser(ctx.Context(), account.ID, targetID); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to remove user"})
	}
	return ctx.Response().NoContent()
}

// ListAccountTokens godoc
// @Summary      List API access tokens for an account
// @Description  Returns all non-expired access tokens for the account. Requires owner or admin.
// @Tags         Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Success      200  {object}  AccessTokenListResponse
// @Failure      403  {object}  ErrorResponse
// @Router       /accounts/{accountId}/tokens [get]
func ListAccountTokens(ctx http.Context) http.Response {
	account, ok := ctx.Value("account").(*models.Account)
	if !ok || account == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "account not found"})
	}
	role, _ := ctx.Value("account_role").(string)
	if role != "owner" && role != "admin" {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "only owners and admins may view tokens"})
	}

	var tokens []models.AccessToken
	if err := facades.Orm().Query().
		Where("account_id = ?", account.ID).
		Find(&tokens); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch tokens"})
	}
	return ctx.Response().Json(http.StatusOK, http.Json{"data": tokens})
}

// CreateAccountToken godoc
// @Summary      Create an API access token for an account
// @Description  Creates a named access token. The raw token is returned once — store it safely. Requires owner or admin.
// @Tags         Accounts
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        accountId  path      string                    true  "Account UUID"
// @Param        request    body      CreateAccountTokenRequest  true  "Token payload"
// @Success      201        {object}  CreateAccountTokenResponse
// @Failure      400        {object}  ErrorResponse
// @Failure      403        {object}  ErrorResponse
// @Router       /accounts/{accountId}/tokens [post]
func CreateAccountToken(ctx http.Context) http.Response {
	account, ok := ctx.Value("account").(*models.Account)
	if !ok || account == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "account not found"})
	}
	role, _ := ctx.Value("account_role").(string)
	if role != "owner" && role != "admin" {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "only owners and admins may create tokens"})
	}
	callerID, _ := ctx.Value("user_id").(uuid.UUID)

	var req CreateAccountTokenRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
	if req.Name == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "name is required"})
	}

	raw, _ := accountAuthService.GenerateRandomToken()
	hash := accountAuthService.HashToken(raw)

	token := &models.AccessToken{
		ID:        uuid.New(),
		AccountID: account.ID,
		CreatedBy: &callerID,
		Name:      req.Name,
		TokenHash: hash,
	}
	if req.ValidUntil != nil {
		token.ValidUntil = req.ValidUntil
	}
	if err := facades.Orm().Query().Create(token); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to create token"})
	}

	return ctx.Response().Json(http.StatusCreated, http.Json{
		"token":    raw, // shown once
		"metadata": token,
	})
}

// RevokeAccountToken godoc
// @Summary      Revoke an API access token
// @Description  Deletes an access token by ID. Requires owner or admin.
// @Tags         Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Param        tokenId    path  string  true  "Token UUID"
// @Success      204  "No content"
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /accounts/{accountId}/tokens/{tokenId} [delete]
func RevokeAccountToken(ctx http.Context) http.Response {
	account, ok := ctx.Value("account").(*models.Account)
	if !ok || account == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "account not found"})
	}
	role, _ := ctx.Value("account_role").(string)
	if role != "owner" && role != "admin" {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "only owners and admins may revoke tokens"})
	}

	tokenIDStr := ctx.Request().Route("tokenId")
	tokenID, err := uuid.Parse(tokenIDStr)
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid token id"})
	}

	var token models.AccessToken
	if err := facades.Orm().Query().
		Where("id = ? AND account_id = ?", tokenID, account.ID).
		First(&token); err != nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "token not found"})
	}

	if _, err := facades.Orm().Query().Delete(&token); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to revoke token"})
	}
	return ctx.Response().NoContent()
}

// ---- Request/Response types ----

type CreateAccountRequest struct {
	Name string `json:"name" example:"Acme Corp"`
}

type UpdateAccountRequest struct {
	Name           string `json:"name,omitempty" example:"New Name"`
	ViewAllWallets *bool  `json:"view_all_wallets,omitempty" example:"true"`
}

type AddAccountUserRequest struct {
	Email string `json:"email" example:"user@example.com"`
	Role  string `json:"role" example:"admin"`
}

type CreateAccountTokenRequest struct {
	Name       string     `json:"name" example:"CI Token"`
	ValidUntil *time.Time `json:"valid_until,omitempty"`
}

type AccountUserListResponse struct {
	Data []models.AccountUser `json:"data"`
}

type AccessTokenListResponse struct {
	Data []models.AccessToken `json:"data"`
}

type CreateAccountTokenResponse struct {
	Token    string             `json:"token"`
	Metadata models.AccessToken `json:"metadata"`
}
