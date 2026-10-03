package accounts

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	mails "github.com/macrowallets/waas/app/mails"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

func validateRequest(ctx http.Context, req http.FormRequest) http.Response {
	return controllers.ValidateRequest(ctx, req)
}

type AccountsController struct {
	accountService *accountsvc.Service
	passwords      *authsvc.Service
}

// NewAccountsController wires the dashboard account handlers. Persistence goes
// through the account service. The auth service only hashes a new API token.
func NewAccountsController(
	accountService *accountsvc.Service,
	passwords *authsvc.Service,
) *AccountsController {
	if accountService == nil {
		panic("dashboard accounts controller: account service is required")
	}
	if passwords == nil {
		panic("dashboard accounts controller: auth service is required")
	}
	return &AccountsController{
		accountService: accountService,
		passwords:      passwords,
	}
}

// CreateAccount godoc
// @Summary      Create a new account
// @Description  Creates an account and makes the caller its owner
// @Tags         Accounts
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        request  body      CreateAccountSwagger  true  "Account payload"
// @Success      201      {object}  models.Account
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Router       /accounts [post]
func (ctrl *AccountsController) CreateAccount(ctx http.Context) http.Response {
	userID := ctx.Value("user_id").(uuid.UUID)

	var req requests.CreateAccountRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	acc, err := ctrl.accountService.Create(ctx.Context(), req.Name, userID)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to create account"})
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
func (ctrl *AccountsController) GetAccount(ctx http.Context) http.Response {
	account := ctx.Value("account").(*models.Account)
	return ctx.Response().Json(http.StatusOK, account)
}

// UpdateAccount godoc
// @Summary      Update account settings
// @Description  Updates account name or view_all_wallets flag. Requires owner or admin role.
// @Tags         Accounts
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        accountId  path      string                      true  "Account UUID"
// @Param        request    body      UpdateAccountSwagger        true  "Update payload"
// @Success      200        {object}  models.Account
// @Failure      400        {object}  ErrorResponse
// @Failure      403        {object}  ErrorResponse
// @Router       /accounts/{accountId} [patch]
func (ctrl *AccountsController) UpdateAccount(ctx http.Context) http.Response {
	account := ctx.Value("account").(*models.Account)
	if errResp := controllers.Deny(ctx, policies.AccountUpdate(ctx, account.ID)); errResp != nil {
		return errResp
	}

	var req requests.UpdateAccountRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	if err := ctrl.accountService.UpdateAccount(ctx.Context(), account, req.Name, req.ViewAllWallets); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to update account"})
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
func (ctrl *AccountsController) ArchiveAccount(ctx http.Context) http.Response {
	account := ctx.Value("account").(*models.Account)
	if errResp := controllers.Deny(ctx, policies.AccountArchive(ctx, account.ID)); errResp != nil {
		return errResp
	}

	if err := ctrl.accountService.SetStatus(ctx.Context(), account, "archived"); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to archive account"})
	}
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
func (ctrl *AccountsController) FreezeAccount(ctx http.Context) http.Response {
	account := ctx.Value("account").(*models.Account)
	if errResp := controllers.Deny(ctx, policies.AccountFreeze(ctx, account.ID)); errResp != nil {
		return errResp
	}

	if err := ctrl.accountService.SetStatus(ctx.Context(), account, "frozen"); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to freeze account"})
	}
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
func (ctrl *AccountsController) ListAccountUsers(ctx http.Context) http.Response {
	account := ctx.Value("account").(*models.Account)

	limit, offset := pagination.ParseParams(ctx, 20)
	members, total, err := ctrl.accountService.ListMembers(ctx.Context(), account.ID, limit, offset)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch members"})
	}
	return ctx.Response().Json(http.StatusOK, pagination.Response(members, total, limit, offset))
}

// AddAccountUser godoc
// @Summary      Add a user to an account
// @Description  Adds a user to the account with the specified role. Requires owner or admin.
// @Tags         Accounts
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        accountId  path      string                     true  "Account UUID"
// @Param        request    body      AddAccountUserSwagger      true  "User and role payload"
// @Success      201        {object}  models.AccountUser
// @Failure      400        {object}  ErrorResponse
// @Failure      403        {object}  ErrorResponse
// @Router       /accounts/{accountId}/users [post]
func (ctrl *AccountsController) AddAccountUser(ctx http.Context) http.Response {
	account := ctx.Value("account").(*models.Account)
	callerID := ctx.Value("user_id").(uuid.UUID)
	if errResp := controllers.Deny(ctx, policies.AccountAddUser(ctx, account.ID)); errResp != nil {
		return errResp
	}

	var req requests.AddAccountUserRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	targetPtr, invited, err := ctrl.accountService.FindOrCreateInvitedUser(ctx.Context(), req.Email)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to create user"})
	}
	if invited {
		if err := facades.Mail().To([]string{req.Email}).Send(&mails.UserInviteMail{
			To:          req.Email,
			InvitedBy:   "your team",
			AccountName: account.Name,
			InviteLink:  "https://vault.app/accept-invite",
		}); err != nil {
			facades.Log().WithContext(ctx).Errorf("account: send invite mail: %v", err)
		}
	}

	if err := ctrl.accountService.AddUser(ctx.Context(), account.ID, targetPtr.ID, req.Role, callerID); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to add user"})
	}

	au, auErr := ctrl.accountService.FindMember(ctx.Context(), account.ID, targetPtr.ID)
	if auErr != nil {
		facades.Log().WithContext(ctx).Errorf("account: find membership after add: %v", auErr)
	}
	return ctx.Response().Json(http.StatusCreated, au)
}

// UpdateAccountUser godoc
// @Summary      Update an account member
// @Description  Changes role and/or status. Owner and admin only. Suspending revokes the member's API tokens for this account.
// @Tags         Accounts
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        accountId  path      string                     true  "Account UUID"
// @Param        userId     path      string                     true  "User UUID"
// @Param        request    body      UpdateAccountUserSwagger   true  "Role and/or status"
// @Success      200        {object}  models.AccountUser
// @Failure      403        {object}  ErrorResponse
// @Failure      404        {object}  ErrorResponse
// @Failure      422        {object}  ErrorResponse
// @Router       /accounts/{accountId}/users/{userId} [patch]
func (ctrl *AccountsController) UpdateAccountUser(ctx http.Context) http.Response {
	account := middleware.AccountFrom(ctx)
	if account == nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
	}
	callerID := middleware.SessionUserID(ctx)
	if callerID == uuid.Nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
	}
	if !policies.ManagesMembers(middleware.AccountRole(ctx)) {
		return responses.Send(ctx, http.StatusForbidden, http.Json{"error": accountsvc.ErrManageMembers.Error()})
	}

	targetID, err := requests.RouteUUID(ctx, "userId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid user id"})
	}

	var req requests.UpdateAccountUserRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	member, err := ctrl.accountService.UpdateMember(ctx.Context(), account.ID, callerID, targetID, memberChange(req))
	if errResp := mapMemberError(ctx, err); errResp != nil {
		return errResp
	}
	return ctx.Response().Json(http.StatusOK, member)
}

func memberChange(req requests.UpdateAccountUserRequest) accountsvc.MemberChange {
	change := accountsvc.MemberChange{}
	if req.Role != "" {
		role := req.Role
		change.Role = &role
	}
	if req.Status != "" {
		status := req.Status
		change.Status = &status
	}
	return change
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
func (ctrl *AccountsController) RemoveAccountUser(ctx http.Context) http.Response {
	account := ctx.Value("account").(*models.Account)
	if errResp := controllers.Deny(ctx, policies.AccountRemoveUser(ctx, account.ID)); errResp != nil {
		return errResp
	}

	callerID := middleware.SessionUserID(ctx)
	if callerID == uuid.Nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
	}
	targetID, err := requests.RouteUUID(ctx, "userId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid user id"})
	}

	if err := ctrl.accountService.RemoveMember(ctx.Context(), account.ID, callerID, targetID); err != nil {
		return mapMemberError(ctx, err)
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
func (ctrl *AccountsController) ListAccountTokens(ctx http.Context) http.Response {
	account := ctx.Value("account").(*models.Account)
	if errResp := controllers.Deny(ctx, policies.AccountManageTokens(ctx, account.ID)); errResp != nil {
		return errResp
	}

	limit, offset := pagination.ParseParams(ctx, 20)
	tokens, total, err := ctrl.accountService.ListAccessTokens(ctx.Context(), account.ID, limit, offset)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch tokens"})
	}
	return ctx.Response().Json(http.StatusOK, pagination.Response(tokens, total, limit, offset))
}

// CreateAccountToken godoc
// @Summary      Create an API access token for an account
// @Description  Creates a named access token. The raw token is returned once — store it safely. Requires owner or admin.
// @Tags         Accounts
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        accountId  path      string                          true  "Account UUID"
// @Param        request    body      CreateAccountTokenSwagger       true  "Token payload"
// @Success      201        {object}  CreateAccountTokenResponse
// @Failure      400        {object}  ErrorResponse
// @Failure      403        {object}  ErrorResponse
// @Router       /accounts/{accountId}/tokens [post]
func (ctrl *AccountsController) CreateAccountToken(ctx http.Context) http.Response {
	account := ctx.Value("account").(*models.Account)
	if errResp := controllers.Deny(ctx, policies.AccountManageTokens(ctx, account.ID)); errResp != nil {
		return errResp
	}
	callerID, _ := ctx.Value("user_id").(uuid.UUID)

	var req requests.CreateAccountTokenRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	tokenID := uuid.New()
	token := &models.AccessToken{
		ID:            tokenID,
		AccountID:     account.ID,
		CreatedBy:     &callerID,
		Name:          req.Name,
		TokenHash:     ctrl.passwords.HashToken(tokenID.String()),
		SpendingLimit: "{}",
	}
	if req.ValidUntil != "" {
		t, _ := time.Parse(time.RFC3339, req.ValidUntil)
		token.ValidUntil = &t
	}
	if err := ctrl.accountService.CreateAccessToken(ctx.Context(), token); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to create token"})
	}

	jwt, err := middleware.MintAPIToken(token, req.RequireSignature)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to sign token"})
	}

	return ctx.Response().Json(http.StatusCreated, http.Json{
		"token":    jwt,
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
func (ctrl *AccountsController) RevokeAccountToken(ctx http.Context) http.Response {
	account := ctx.Value("account").(*models.Account)
	if errResp := controllers.Deny(ctx, policies.AccountManageTokens(ctx, account.ID)); errResp != nil {
		return errResp
	}

	tokenID, err := requests.RouteUUID(ctx, "tokenId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid token id"})
	}

	if err := ctrl.accountService.RevokeAccessToken(ctx.Context(), account.ID, tokenID); err != nil {
		if errors.Is(err, accountsvc.ErrAccessTokenNotFound) {
			return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "token not found"})
		}
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to revoke token"})
	}
	return ctx.Response().NoContent()
}

// ---- Swagger-only types (keep for @Param annotations) ----

type CreateAccountSwagger struct {
	Name string `json:"name" example:"Acme Corp"`
}

type UpdateAccountSwagger struct {
	Name           string `json:"name,omitempty" example:"New Name"`
	ViewAllWallets *bool  `json:"view_all_wallets,omitempty" example:"true"`
}

type AddAccountUserSwagger struct {
	Email string `json:"email" example:"user@example.com"`
	Role  string `json:"role" example:"admin"`
}

type UpdateAccountUserSwagger struct {
	Role   string `json:"role,omitempty" example:"admin"`
	Status string `json:"status,omitempty" example:"suspended"`
}

type CreateAccountTokenSwagger struct {
	Name             string     `json:"name" example:"CI Token"`
	ValidUntil       *time.Time `json:"valid_until,omitempty"`
	RequireSignature bool       `json:"require_signature,omitempty" example:"true"`
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
