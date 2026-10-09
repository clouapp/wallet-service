package accounts

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	resources "github.com/macrowallets/waas/app/http/resources/platform/accounts"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// defaultLimit matches GET /v1/platform/users: an omitted limit is 20 and an
// omitted offset is 0.
const defaultLimit = 20

// AccountController serves GET /v1/platform/accounts and
// POST /v1/platform/accounts/{accountId}/freeze, /unfreeze, and /archive.
// S3.4.1 names accounts.view and accounts.lifecycle. A platform_admins row is
// the gate. The routes are not behind AccountContext, so a frozen or archived
// account can still be changed.
type AccountController struct {
	accounts *accountsvc.Service
}

// NewAccountController wires the platform account handlers.
func NewAccountController(accounts *accountsvc.Service) *AccountController {
	if accounts == nil {
		panic("platform account controller: account service is required")
	}
	return &AccountController{accounts: accounts}
}

// Index godoc
//
//	@Summary		List platform accounts
//	@Description	Newest created_at first. Permission accounts.view; a platform admin may call it. The row is id, name, and status.
//	@Tags			Platform Accounts
//	@Security		BearerAuth
//	@Produce		json
//	@Param			limit	query		int	false	"Page size"
//	@Param			offset	query		int	false	"Rows to skip"
//	@Success		200		{object}	map[string]any
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		403		{object}	responses.ErrorBody
//	@Router			/platform/accounts [get]
func (c *AccountController) Index(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	limit, offset := pagination.ParseParams(ctx, defaultLimit)

	rows, total, err := c.accounts.ListForPlatform(ctx.Context(), actorID, limit, offset)
	if err != nil {
		return mapError(ctx, err, "list platform accounts")
	}

	return ctx.Response().Success().Json(pagination.Response(resources.AccountsFrom(rows), total, limit, offset))
}

// Freeze godoc
//
//	@Summary		Freeze an account
//	@Description	Sets accounts.status to frozen. Only a platform admin may call it. An unknown account is 404 for a platform admin. The same status again does not write. The route is not behind AccountContext.
//	@Tags			Platform Accounts
//	@Security		BearerAuth
//	@Produce		json
//	@Param			accountId	path		string	true	"Account UUID"
//	@Success		200			{object}	resources.Lifecycle
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		401			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/platform/accounts/{accountId}/freeze [post]
func (c *AccountController) Freeze(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	accountID, err := requests.RouteUUID(ctx, "accountId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid account id")
	}

	account, err := c.accounts.SetPlatformLifecycle(ctx.Context(), actorID, accountID, models.AccountStatusFrozen)
	if err != nil {
		return mapError(ctx, err, "freeze platform account")
	}

	return ctx.Response().Success().Json(resources.NewLifecycle(account))
}

// Unfreeze godoc
//
//	@Summary		Unfreeze an account
//	@Description	Sets accounts.status to active. Only a platform admin may call it. An unknown account is 404 for a platform admin. The same status again does not write. The route is not behind AccountContext, so a frozen account can be unfrozen.
//	@Tags			Platform Accounts
//	@Security		BearerAuth
//	@Produce		json
//	@Param			accountId	path		string	true	"Account UUID"
//	@Success		200			{object}	resources.Lifecycle
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		401			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/platform/accounts/{accountId}/unfreeze [post]
func (c *AccountController) Unfreeze(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	accountID, err := requests.RouteUUID(ctx, "accountId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid account id")
	}

	account, err := c.accounts.SetPlatformLifecycle(ctx.Context(), actorID, accountID, models.StatusActive)
	if err != nil {
		return mapError(ctx, err, "unfreeze platform account")
	}

	return ctx.Response().Success().Json(resources.NewLifecycle(account))
}

// Archive godoc
//
//	@Summary		Archive an account
//	@Description	Sets accounts.status to archived. Only a platform admin may call it. An unknown account is 404 for a platform admin. The same status again does not write. The route is not behind AccountContext.
//	@Tags			Platform Accounts
//	@Security		BearerAuth
//	@Produce		json
//	@Param			accountId	path		string	true	"Account UUID"
//	@Success		200			{object}	resources.Lifecycle
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		401			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/platform/accounts/{accountId}/archive [post]
func (c *AccountController) Archive(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	accountID, err := requests.RouteUUID(ctx, "accountId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid account id")
	}

	account, err := c.accounts.SetPlatformLifecycle(ctx.Context(), actorID, accountID, models.AccountStatusArchived)
	if err != nil {
		return mapError(ctx, err, "archive platform account")
	}

	return ctx.Response().Success().Json(resources.NewLifecycle(account))
}
