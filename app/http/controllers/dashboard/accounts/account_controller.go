package accounts

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	tokenresource "github.com/macrowallets/waas/app/http/resources/dashboard/accounts"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/credentialmail"
	featuressvc "github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/withdraw"
)

func validateRequest(ctx http.Context, req http.FormRequest) http.Response {
	return controllers.ValidateRequest(ctx, req)
}

type AccountsController struct {
	accountService *accountsvc.Service
	passwords      *authsvc.Service
	limits         *settings.Service
	features       *featuressvc.Service
	credentialMail *credentialmail.Service
}

// NewAccountsController wires the dashboard account handlers. Persistence goes
// through the account service. The auth service only turns a new API token
// secret into its sha256 digest. Limits supplies sweep_limits from settings.
// Features supplies the active flag keys on GET.
func NewAccountsController(
	accountService *accountsvc.Service,
	passwords *authsvc.Service,
	limits *settings.Service,
	features *featuressvc.Service,
	credentialMail *credentialmail.Service,
) *AccountsController {
	if accountService == nil {
		panic("dashboard accounts controller: account service is required")
	}
	if passwords == nil {
		panic("dashboard accounts controller: auth service is required")
	}
	if limits == nil {
		panic("dashboard accounts controller: settings service is required")
	}
	if features == nil {
		panic("dashboard accounts controller: features service is required")
	}
	if credentialMail == nil {
		panic("dashboard accounts controller: credential mail is required")
	}
	return &AccountsController{
		accountService: accountService,
		passwords:      passwords,
		limits:         limits,
		features:       features,
		credentialMail: credentialMail,
	}
}

func (ctrl *AccountsController) accountView(ctx http.Context, account models.Account) (tokenresource.Account, error) {
	view := tokenresource.AccountFrom(account)
	document, err := ctrl.limits.AccountSweepLimitsWire(ctx.Context(), account.ID)
	if err != nil {
		return tokenresource.Account{}, err
	}
	view.SweepLimits = document
	return view, nil
}

// CreateAccount godoc
// @Summary      Create a new account
// @Description  Creates an account and makes the caller its owner
// @Tags         Accounts
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        request  body      CreateAccountSwagger  true  "Account payload"
// @Success      201      {object}  tokenresource.Account
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Router       /accounts [post]
func (ctrl *AccountsController) CreateAccount(ctx http.Context) http.Response {
	userID := requestctx.MustUserID(ctx)

	var req requests.CreateAccountRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	acc, err := ctrl.accountService.Create(ctx.Context(), req.Name, userID)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to create account"})
	}
	view, err := ctrl.accountView(ctx, *acc)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to create account"})
	}
	return responses.Send(ctx, http.StatusCreated, view)
}

// GetAccount godoc
// @Summary      Get an account
// @Description  Returns account details and the account's active feature keys. Requires account membership (injected by AccountContext middleware).
// @Tags         Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path      string         true  "Account UUID"
// @Success      200        {object}  AccountDetail
// @Failure      403        {object}  ErrorResponse
// @Failure      404        {object}  ErrorResponse
// @Router       /accounts/{accountId} [get]
func (ctrl *AccountsController) GetAccount(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	view, err := ctrl.accountView(ctx, *account)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch account"})
	}
	names, err := ctrl.features.ActiveForAccount(ctx.Context(), account.ID)
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("account: active features: %v", err)
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch account"})
	}
	return responses.Send(ctx, http.StatusOK, AccountDetail{Account: view, Features: names})
}

// AccountDetail is GET /v1/accounts/{accountId}. Existing account fields stay.
// Features is the account's active flag keys in catalog order. A missing
// account row uses the catalog default. Global rows are not included. Create
// and update do not carry this field.
type AccountDetail struct {
	tokenresource.Account
	Features []string `json:"features"`
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
// @Success      200        {object}  tokenresource.Account
// @Failure      400        {object}  ErrorResponse
// @Failure      403        {object}  ErrorResponse
// @Router       /accounts/{accountId} [patch]
func (ctrl *AccountsController) UpdateAccount(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	if errResp := controllers.Deny(ctx, policies.AccountUpdate(ctx, account.ID, middleware.SessionUserID(ctx))); errResp != nil {
		return errResp
	}

	var req requests.UpdateAccountRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	if err := ctrl.accountService.UpdateAccount(ctx.Context(), account, req.Name, req.ViewAllWallets); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to update account"})
	}

	view, err := ctrl.accountView(ctx, *account)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to update account"})
	}
	return responses.Send(ctx, http.StatusOK, view)
}

// ArchiveAccount godoc
// @Summary      Archive an account
// @Description  Sets account status to 'archived'. Requires owner role.
// @Tags         Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Success      200        {object}  tokenresource.Account
// @Failure      403        {object}  ErrorResponse
// @Failure      404        {object}  ErrorResponse
// @Router       /accounts/{accountId}/archive [post]
func (ctrl *AccountsController) ArchiveAccount(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	if errResp := controllers.Deny(ctx, policies.AccountArchive(ctx, account.ID, middleware.SessionUserID(ctx))); errResp != nil {
		return errResp
	}

	if err := ctrl.accountService.SetStatus(ctx.Context(), account, "archived"); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to archive account"})
	}
	view, err := ctrl.accountView(ctx, *account)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to archive account"})
	}
	return responses.Send(ctx, http.StatusOK, view)
}

// FreezeAccount godoc
// @Summary      Freeze an account
// @Description  Sets account status to 'frozen'. Requires owner role.
// @Tags         Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Success      200        {object}  tokenresource.Account
// @Failure      403        {object}  ErrorResponse
// @Router       /accounts/{accountId}/freeze [post]
func (ctrl *AccountsController) FreezeAccount(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	if errResp := controllers.Deny(ctx, policies.AccountFreeze(ctx, account.ID, middleware.SessionUserID(ctx))); errResp != nil {
		return errResp
	}

	if err := ctrl.accountService.SetStatus(ctx.Context(), account, "frozen"); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to freeze account"})
	}
	view, err := ctrl.accountView(ctx, *account)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to freeze account"})
	}
	return responses.Send(ctx, http.StatusOK, view)
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
	account := requestctx.MustAccount(ctx)

	limit, offset := pagination.ParseParams(ctx, 20)
	members, total, err := ctrl.accountService.ListMembers(ctx.Context(), account.ID, limit, offset)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch members"})
	}
	return responses.Send(ctx, http.StatusOK, pagination.Response(tokenresource.AccountUsersFrom(members), total, limit, offset))
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
// @Success      201        {object}  tokenresource.AccountUser
// @Failure      400        {object}  ErrorResponse
// @Failure      403        {object}  ErrorResponse
// @Router       /accounts/{accountId}/users [post]
func (ctrl *AccountsController) AddAccountUser(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	callerID := requestctx.MustUserID(ctx)
	if errResp := controllers.Deny(ctx, policies.AccountAddUser(ctx, account.ID, middleware.SessionUserID(ctx))); errResp != nil {
		return errResp
	}

	var req requests.AddAccountUserRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	targetPtr, findErr := ctrl.accountService.FindUserByEmail(ctx.Context(), req.Email)
	if findErr != nil && !errors.Is(findErr, models.ErrRepositoryNotFound) {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to look up user"})
	}
	if targetPtr == nil || errors.Is(findErr, models.ErrRepositoryNotFound) {
		base, errResp := requireFrontendBase(ctx, "failed to create invite")
		if errResp != nil {
			return errResp
		}
		issued, issueErr := ctrl.accountService.IssueInvite(ctx.Context(), account.ID, req.Email, req.Role, callerID, base)
		if issueErr != nil {
			if errors.Is(issueErr, accountsvc.ErrGrantRole) {
				return responses.Send(ctx, http.StatusForbidden, http.Json{"error": issueErr.Error()})
			}
			return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to create invite"})
		}
		link, mailErr := ctrl.credentialMail.DispatchAccountInvite(issued.Invite.ID)
		if mailErr != nil {
			appfacades.Log().WithContext(ctx).Errorf("account: send invite mail failed")
		}
		if link == "" {
			link = issued.InviteLink
		}
		return responses.Send(ctx, http.StatusAccepted, http.Json{
			"invite_id":   issued.Invite.ID,
			"email":       issued.Invite.Email,
			"role":        issued.Invite.Role,
			"expires_at":  issued.Invite.ExpiresAt,
			"invite_link": link,
		})
	}

	if err := ctrl.accountService.AddUser(ctx.Context(), account.ID, targetPtr.ID, req.Role, callerID); err != nil {
		if errors.Is(err, accountsvc.ErrGrantRole) {
			return responses.Send(ctx, http.StatusForbidden, http.Json{"error": err.Error()})
		}
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to add user"})
	}

	au, auErr := ctrl.accountService.FindMember(ctx.Context(), account.ID, targetPtr.ID)
	if auErr != nil {
		appfacades.Log().WithContext(ctx).Errorf("account: find membership after add: %v", auErr)
	}
	return responses.Send(ctx, http.StatusCreated, tokenresource.AccountUserPtr(au))
}

// UpdateAccountUser godoc
// @Summary      Update an account member
// @Description  Changes role and/or status. Owner and admin only. Suspending leaves the API tokens that member minted for this account.
// @Tags         Accounts
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        accountId  path      string                     true  "Account UUID"
// @Param        userId     path      string                     true  "User UUID"
// @Param        request    body      UpdateAccountUserSwagger   true  "Role and/or status"
// @Success      200        {object}  tokenresource.AccountUser
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
	return responses.Send(ctx, http.StatusOK, tokenresource.AccountUserPtr(member))
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
	account := requestctx.MustAccount(ctx)
	if errResp := controllers.Deny(ctx, policies.AccountRemoveUser(ctx, account.ID, middleware.SessionUserID(ctx))); errResp != nil {
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
// @Description  Returns the account's API tokens. Requires tokens.read.
// @Tags         Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Success      200  {object}  AccessTokenListResponse
// @Failure      403  {object}  ErrorResponse
// @Router       /accounts/{accountId}/tokens [get]
func (ctrl *AccountsController) ListAccountTokens(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	if errResp := controllers.Deny(ctx, policies.AccountReadTokens(ctx, account.ID, middleware.SessionUserID(ctx))); errResp != nil {
		return errResp
	}

	limit, offset := pagination.ParseParams(ctx, 20)
	tokens, total, err := ctrl.accountService.ListAccessTokens(ctx.Context(), account.ID, limit, offset)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch tokens"})
	}
	return responses.Send(ctx, http.StatusOK, pagination.Response(tokenresource.AccessTokensFrom(tokens), total, limit, offset))
}

// CreateAccountToken godoc
// @Summary      Create an API access token for an account
// @Description  Creates a named access token. The raw token is returned once — store it safely. Requires tokens.write.
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
	account := requestctx.MustAccount(ctx)
	if errResp := controllers.Deny(ctx, policies.AccountWriteTokens(ctx, account.ID, middleware.SessionUserID(ctx))); errResp != nil {
		return errResp
	}
	callerID, _ := requestctx.UserID(ctx)

	var req requests.CreateAccountTokenRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}
	if errResp := controllers.Deny(ctx, policies.MintAPITokenPermissions(middleware.AccountRole(ctx), req.Permissions)); errResp != nil {
		return errResp
	}
	if !policies.ValidAPITokenIPCIDR(req.IpCidr) {
		return responses.FieldsFailed(ctx, map[string][]string{
			"ip_cidr": {"The ip_cidr must be a valid CIDR."},
		})
	}
	storedPermissions, err := storedAPITokenPermissions(req.Permissions)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to create token"})
	}
	storedLimit, err := withdraw.StoreSpendingLimit(req.SpendingLimit)
	if err != nil {
		field := "spending_limit"
		message := "must be an object with an optional daily_usd decimal"
		if errors.Is(err, withdraw.ErrNegativeSpendingLimit) {
			field = "spending_limit.daily_usd"
			message = "must be a decimal string greater than or equal to 0"
		}
		return responses.FieldsFailed(ctx, map[string][]string{field: {message}})
	}

	secret, err := ctrl.passwords.GenerateAPITokenSecret()
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to create token"})
	}
	tokenID := uuid.New()
	token := &models.AccessToken{
		ID:            tokenID,
		AccountID:     account.ID,
		CreatedBy:     &callerID,
		Name:          req.Name,
		TokenHash:     ctrl.passwords.HashAPITokenSecret(secret),
		Permissions:   storedPermissions,
		IpCidr:        strings.TrimSpace(req.IpCidr),
		SpendingLimit: storedLimit,
	}
	if req.ValidUntil != "" {
		t, _ := time.Parse(time.RFC3339, req.ValidUntil)
		token.ValidUntil = &t
	}
	if err := ctrl.accountService.CreateAccessToken(ctx.Context(), token); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to create token"})
	}

	jwt, err := middleware.MintAPITokenWithSecret(token, req.RequireSignature, secret)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to sign token"})
	}

	return responses.Send(ctx, http.StatusCreated, http.Json{
		"token":    jwt,
		"metadata": tokenresource.AccessTokenPtr(token),
	})
}

// RevokeAccountToken godoc
// @Summary      Revoke an API access token
// @Description  Soft-revokes an access token by ID. The row stays for audit. Requires tokens.write.
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
	account := requestctx.MustAccount(ctx)
	if errResp := controllers.Deny(ctx, policies.AccountWriteTokens(ctx, account.ID, middleware.SessionUserID(ctx))); errResp != nil {
		return errResp
	}

	tokenID, err := requests.RouteUUID(ctx, "tokenId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid token id"})
	}

	callerID, _ := requestctx.UserID(ctx)
	if err := ctrl.accountService.RevokeAccessToken(ctx.Context(), account.ID, callerID, tokenID); err != nil {
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
	Permissions      []string   `json:"permissions,omitempty"`
	IpCidr           string     `json:"ip_cidr,omitempty" example:"192.0.2.0/24"`
}

type AccountUserListResponse struct {
	Data []tokenresource.AccountUser `json:"data"`
}

type AccessTokenListResponse struct {
	Data []tokenresource.AccessToken `json:"data"`
}

type CreateAccountTokenResponse struct {
	Token    string                    `json:"token"`
	Metadata tokenresource.AccessToken `json:"metadata"`
}
