package accounts

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	accountsrequests "github.com/macrowallets/waas/app/http/requests/dashboard/accounts"
	resources "github.com/macrowallets/waas/app/http/resources/dashboard/accounts"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// AccountController creates an account and reads, changes, archives and
// freezes the account in scope. account.write and account.lifecycle are route
// middleware.
type AccountController struct {
	accounts *accountsvc.Service
}

// NewAccountController wires the account handlers to the account service,
// which reads each account's sweep limits and feature flags for its view.
func NewAccountController(accounts *accountsvc.Service) *AccountController {
	if accounts == nil {
		panic("dashboard account controller: account service is required")
	}
	return &AccountController{accounts: accounts}
}

// Store godoc
//
//	@Summary		Create a new account
//	@Description	Creates an account and makes the caller its owner
//	@Tags			Accounts
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		CreateAccountSwagger	true	"Account payload"
//	@Success		201		{object}	resources.Account
//	@Failure		400		{object}	responses.ErrorBody
//	@Failure		401		{object}	responses.ErrorBody
//	@Router			/accounts [post]
func (c *AccountController) Store(ctx http.Context) http.Response {
	userID := requestctx.MustUserID(ctx)

	var req accountsrequests.CreateAccountRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	view, err := c.accounts.CreateForOwner(ctx.Context(), accountsvc.CreateAccountInput{Name: req.Name, OwnerID: userID})
	if err != nil {
		return mapError(ctx, err, "failed to create account")
	}

	return ctx.Response().Status(http.StatusCreated).Json(resources.NewAccount(view))
}

// Show godoc
//
//	@Summary		Get an account
//	@Description	Returns account details and the account's active feature keys. Requires account membership (injected by AccountContext middleware).
//	@Tags			Accounts
//	@Security		BearerAuth
//	@Produce		json
//	@Param			accountId	path		string	true	"Account UUID"
//	@Success		200			{object}	resources.AccountDetail
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/accounts/{accountId} [get]
func (c *AccountController) Show(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)

	detail, err := c.accounts.Detail(ctx.Context(), account)
	if err != nil {
		return mapError(ctx, err, "failed to fetch account")
	}

	return ctx.Response().Success().Json(resources.NewAccountDetail(detail))
}

// Update godoc
//
//	@Summary		Update account settings
//	@Description	Updates account name or view_all_wallets flag. Requires owner or admin role.
//	@Tags			Accounts
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			accountId	path		string					true	"Account UUID"
//	@Param			request		body		UpdateAccountSwagger	true	"Update payload"
//	@Success		200			{object}	resources.Account
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Router			/accounts/{accountId} [patch]
func (c *AccountController) Update(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)

	var req accountsrequests.UpdateAccountRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	view, err := c.accounts.UpdateAccount(ctx.Context(), account, accountsvc.UpdateAccountInput{
		Name:           req.Name,
		ViewAllWallets: req.ViewAllWallets,
	})
	if err != nil {
		return mapError(ctx, err, "failed to update account")
	}

	return ctx.Response().Success().Json(resources.NewAccount(view))
}

// Archive godoc
//
//	@Summary		Archive an account
//	@Description	Sets account status to 'archived'. Requires owner role.
//	@Tags			Accounts
//	@Security		BearerAuth
//	@Produce		json
//	@Param			accountId	path		string	true	"Account UUID"
//	@Success		200			{object}	resources.Account
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/accounts/{accountId}/archive [post]
func (c *AccountController) Archive(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)

	view, err := c.accounts.Archive(ctx.Context(), account)
	if err != nil {
		return mapError(ctx, err, "failed to archive account")
	}

	return ctx.Response().Success().Json(resources.NewAccount(view))
}

// Freeze godoc
//
//	@Summary		Freeze an account
//	@Description	Sets account status to 'frozen'. Requires owner role.
//	@Tags			Accounts
//	@Security		BearerAuth
//	@Produce		json
//	@Param			accountId	path		string	true	"Account UUID"
//	@Success		200			{object}	resources.Account
//	@Failure		403			{object}	responses.ErrorBody
//	@Router			/accounts/{accountId}/freeze [post]
func (c *AccountController) Freeze(ctx http.Context) http.Response {
	account := requestctx.MustAccount(ctx)

	view, err := c.accounts.Freeze(ctx.Context(), account)
	if err != nil {
		return mapError(ctx, err, "failed to freeze account")
	}

	return ctx.Response().Success().Json(resources.NewAccount(view))
}
