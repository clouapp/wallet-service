package accounts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	accountsrequests "github.com/macrowallets/waas/app/http/requests/dashboard/accounts"
	tokenresource "github.com/macrowallets/waas/app/http/resources/dashboard/accounts"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	featuressvc "github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/settings"
)

type AccountsController struct {
	accountService *accountsvc.Service
	limits         *settings.Service
	features       *featuressvc.Service
}

// AccountsControllerDeps is everything the dashboard accounts controller needs.
// Persistence goes through AccountService. Limits supplies sweep_limits from settings.
// Features supplies the active flag keys on GET. Every field is required.
// Invite mail is dispatched by the account service.
type AccountsControllerDeps struct {
	AccountService *accountsvc.Service
	Limits         *settings.Service
	Features       *featuressvc.Service
}

// NewAccountsController wires the dashboard account handlers from AccountsControllerDeps.
func NewAccountsController(deps AccountsControllerDeps) *AccountsController {
	if deps.AccountService == nil {
		panic("dashboard accounts controller: account service is required")
	}
	if deps.Limits == nil {
		panic("dashboard accounts controller: settings service is required")
	}
	if deps.Features == nil {
		panic("dashboard accounts controller: features service is required")
	}
	return &AccountsController{
		accountService: deps.AccountService,
		limits:         deps.Limits,
		features:       deps.Features,
	}
}

func (ctrl *AccountsController) accountView(ctx http.Context, account models.Account) (tokenresource.Account, error) {
	return controllers.AccountView(ctx.Context(), ctrl.limits, account)
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
// @Failure      400      {object}  responses.ErrorBody
// @Failure      401      {object}  responses.ErrorBody
// @Router       /accounts [post]
func (ctrl *AccountsController) CreateAccount(ctx http.Context) http.Response {
	userID := requestctx.MustUserID(ctx)

	var req accountsrequests.CreateAccountRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}

	acc, err := ctrl.accountService.Create(ctx.Context(), req.Name, userID)
	if err != nil {
		return responses.InternalError(ctx, err)
	}
	view, err := ctrl.accountView(ctx, *acc)
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to create account")
	}
	return ctx.Response().Status(http.StatusCreated).Json(view)
}

// GetAccount godoc
// @Summary      Get an account
// @Description  Returns account details and the account's active feature keys. Requires account membership (injected by AccountContext middleware).
// @Tags         Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path      string         true  "Account UUID"
// @Success      200        {object}  AccountDetail
// @Failure      403        {object}  responses.ErrorBody
// @Failure      404        {object}  responses.ErrorBody
// @Router       /accounts/{accountId} [get]
func (ctrl *AccountsController) GetAccount(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	view, err := ctrl.accountView(ctx, *account)
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to fetch account")
	}
	names, err := ctrl.features.ActiveForAccount(ctx.Context(), account.ID)
	if err != nil {
		logActiveFeaturesFailure(ctx, err)
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to fetch account")
	}
	return ctx.Response().Success().Json(AccountDetail{Account: view, Features: names})
}

// logActiveFeaturesFailure records that the account's active feature keys
// could not be read. The request context holds the session JWT; that value
// is not written.
func logActiveFeaturesFailure(_ context.Context, err error) {
	slog.Error(fmt.Sprintf("account: active features: %v", err))
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
// @Failure      400        {object}  responses.ErrorBody
// @Failure      403        {object}  responses.ErrorBody
// @Router       /accounts/{accountId} [patch]
func (ctrl *AccountsController) UpdateAccount(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)

	var req accountsrequests.UpdateAccountRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}

	if err := ctrl.accountService.UpdateAccount(ctx.Context(), account, req.Name, req.ViewAllWallets); err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to update account")
	}

	view, err := ctrl.accountView(ctx, *account)
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to update account")
	}
	return ctx.Response().Success().Json(view)
}

// ArchiveAccount godoc
// @Summary      Archive an account
// @Description  Sets account status to 'archived'. Requires owner role.
// @Tags         Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Success      200        {object}  tokenresource.Account
// @Failure      403        {object}  responses.ErrorBody
// @Failure      404        {object}  responses.ErrorBody
// @Router       /accounts/{accountId}/archive [post]
func (ctrl *AccountsController) ArchiveAccount(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)

	if err := ctrl.accountService.SetStatus(ctx.Context(), account, "archived"); err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to archive account")
	}
	view, err := ctrl.accountView(ctx, *account)
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to archive account")
	}
	return ctx.Response().Success().Json(view)
}

// FreezeAccount godoc
// @Summary      Freeze an account
// @Description  Sets account status to 'frozen'. Requires owner role.
// @Tags         Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Success      200        {object}  tokenresource.Account
// @Failure      403        {object}  responses.ErrorBody
// @Router       /accounts/{accountId}/freeze [post]
func (ctrl *AccountsController) FreezeAccount(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)

	if err := ctrl.accountService.SetStatus(ctx.Context(), account, "frozen"); err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to freeze account")
	}
	view, err := ctrl.accountView(ctx, *account)
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to freeze account")
	}
	return ctx.Response().Success().Json(view)
}

// ListAccountUsers godoc
// @Summary      List account members
// @Description  Returns all active members of the account with their roles
// @Tags         Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path  string  true  "Account UUID"
// @Success      200        {object}  AccountUserListResponse
// @Failure      403        {object}  responses.ErrorBody
// @Router       /accounts/{accountId}/users [get]
func (ctrl *AccountsController) ListAccountUsers(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)

	limit, offset := pagination.ParseParams(ctx, 20)
	members, total, err := ctrl.accountService.ListMembers(ctx.Context(), account.ID, limit, offset)
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to fetch members")
	}
	return ctx.Response().Success().Json(pagination.Response(tokenresource.AccountUsersFrom(members), total, limit, offset))
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
// @Failure      400        {object}  responses.ErrorBody
// @Failure      403        {object}  responses.ErrorBody
// @Router       /accounts/{accountId}/users [post]
func (ctrl *AccountsController) AddAccountUser(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	callerID := requestctx.MustUserID(ctx)
	var req accountsrequests.AddAccountUserRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}

	targetPtr, findErr := ctrl.accountService.FindUserByEmail(ctx.Context(), req.Email)
	if findErr != nil && !errors.Is(findErr, models.ErrRepositoryNotFound) {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to look up user")
	}
	if targetPtr == nil || errors.Is(findErr, models.ErrRepositoryNotFound) {
		base, errResp := requireFrontendBase(ctx, "failed to create invite")
		if errResp != nil {
			return errResp
		}
		issued, issueErr := ctrl.accountService.IssueInvite(ctx.Context(), account.ID, req.Email, req.Role, callerID, base)
		if issueErr != nil {
			if errors.Is(issueErr, accountsvc.ErrGrantRole) {
				return inviteGrantForbidden(ctx, issueErr)
			}
			return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to create invite")
		}
		if issued.MailErr != nil {
			appfacades.Log().WithContext(ctx).Errorf("account: send invite mail failed")
		}
		return ctx.Response().Status(http.StatusAccepted).Json(http.Json{
			"invite_id":   issued.Invite.ID,
			"email":       issued.Invite.Email,
			"role":        issued.Invite.Role,
			"expires_at":  issued.Invite.ExpiresAt,
			"invite_link": issued.InviteLink,
		})
	}

	if err := ctrl.accountService.AddUser(ctx.Context(), account.ID, targetPtr.ID, req.Role, callerID); err != nil {
		if errors.Is(err, accountsvc.ErrGrantRole) {
			return inviteGrantForbidden(ctx, err)
		}
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to add user")
	}

	au, auErr := ctrl.accountService.FindMember(ctx.Context(), account.ID, targetPtr.ID)
	if auErr != nil && !errors.Is(auErr, models.ErrRepositoryNotFound) {
		appfacades.Log().WithContext(ctx).Errorf("account: find membership after add: %v", auErr)
		return responses.Fail(ctx, http.StatusForbidden, responses.CodeForbidden, "not a member of this account")
	}
	return ctx.Response().Status(http.StatusCreated).Json(tokenresource.AccountUserPtr(au))
}

// inviteGrantForbidden answers a role the caller cannot grant. ErrGrantRole may
// be wrapped, and that wrap can carry a query or the address the customer
// typed, so the body stays the fixed envelope and the log keeps the type.
func inviteGrantForbidden(ctx http.Context, err error) http.Response {
	slog.Error("account invite grant refused", "error_type", fmt.Sprintf("%T", err))
	return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, "forbidden")
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
// @Failure      403        {object}  responses.ErrorBody
// @Failure      404        {object}  responses.ErrorBody
// @Failure      422        {object}  responses.ErrorBody
// @Router       /accounts/{accountId}/users/{userId} [patch]
func (ctrl *AccountsController) UpdateAccountUser(ctx http.Context) http.Response {
	account := middleware.AccountFrom(ctx)
	if account == nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternalError, "internal_error")
	}
	callerID := middleware.SessionUserID(ctx)
	if callerID == uuid.Nil {
		return responses.Fail(ctx, http.StatusUnauthorized, "unauthenticated", "unauthenticated")
	}

	targetID, err := requests.RouteUUID(ctx, "userId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid user id")
	}

	var req accountsrequests.UpdateAccountUserRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}

	member, err := ctrl.accountService.UpdateMember(ctx.Context(), accountsvc.UpdateMemberInput{
		AccountID: account.ID,
		ActorID:   callerID,
		TargetID:  targetID,
		Role:      req.Role,
		Status:    req.Status,
	})
	if errResp := mapMemberError(ctx, err); errResp != nil {
		return errResp
	}
	return ctx.Response().Success().Json(tokenresource.AccountUserPtr(member))
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
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /accounts/{accountId}/users/{userId} [delete]
func (ctrl *AccountsController) RemoveAccountUser(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)
	callerID := middleware.SessionUserID(ctx)
	if callerID == uuid.Nil {
		return responses.Fail(ctx, http.StatusUnauthorized, "unauthenticated", "unauthenticated")
	}
	targetID, err := requests.RouteUUID(ctx, "userId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid user id")
	}

	if err := ctrl.accountService.RemoveMember(ctx.Context(), account.ID, callerID, targetID); err != nil {
		return mapMemberError(ctx, err)
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

type AccountUserListResponse struct {
	Data []tokenresource.AccountUser `json:"data"`
}
