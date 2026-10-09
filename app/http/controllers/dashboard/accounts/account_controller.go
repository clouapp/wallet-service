package accounts

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
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

// ---- Swagger-only types (keep for @Param annotations) ----

type CreateAccountSwagger struct {
	Name string `json:"name" example:"Acme Corp"`
}

type UpdateAccountSwagger struct {
	Name           string `json:"name,omitempty" example:"New Name"`
	ViewAllWallets *bool  `json:"view_all_wallets,omitempty" example:"true"`
}
