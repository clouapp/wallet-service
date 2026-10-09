package users

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	usersrequests "github.com/macrowallets/waas/app/http/requests/dashboard/users"
	accountresources "github.com/macrowallets/waas/app/http/resources/dashboard/accounts"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// AccountController lists the accounts the caller belongs to and sets which
// one is the caller's default.
type AccountController struct {
	accounts *accountsvc.Service
}

// NewAccountController wires the caller's account handlers to the account service.
func NewAccountController(accounts *accountsvc.Service) *AccountController {
	if accounts == nil {
		panic("dashboard user account controller: account service is required")
	}
	return &AccountController{accounts: accounts}
}

// Index godoc
//
//	@Summary		List accounts for current user
//	@Description	Returns a paginated list of accounts the authenticated user is a member of, ordered by name. A limit above 100 is capped; an offset past the end returns an empty page with the real total.
//	@Tags			User
//	@Security		BearerAuth
//	@Produce		json
//	@Param			limit		query		int		false	"Page size, 1-100 (default 20)"		example(20)
//	@Param			offset		query		int		false	"Rows to skip, >= 0 (default 0)"	example(0)
//	@Param			search		query		string	false	"Case-insensitive match on name or id (max 100 chars)"
//	@Param			environment	query		string	false	"Only accounts in this environment"	Enums(prod, test)
//	@Success		200			{object}	AccountListResponse
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		401			{object}	responses.ErrorBody
//	@Router			/users/me/accounts [get]
func (c *AccountController) Index(ctx http.Context) http.Response {
	userID := requestctx.MustUserID(ctx)

	query, err := usersrequests.ParseListAccounts(ctx)
	if err != nil {
		return mapError(ctx, err, "failed to fetch accounts")
	}

	members, total, err := c.accounts.ListForMemberWithRoles(ctx.Context(), userID, query.Search, query.Environment, query.Limit, query.Offset)
	if err != nil {
		return mapError(ctx, err, "failed to fetch accounts")
	}

	return ctx.Response().Success().Json(pagination.Response(accountresources.NewMemberAccounts(members), total, query.Limit, query.Offset))
}

// UpdateDefault godoc
//
//	@Summary		Set default account
//	@Description	Updates the authenticated user's default account
//	@Tags			User
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		UpdateDefaultAccountSwagger	true	"Default account payload"
//	@Success		200		{object}	map[string]interface{}
//	@Failure		400		{object}	responses.ErrorBody
//	@Failure		403		{object}	responses.ErrorBody
//	@Router			/users/me/default-account [patch]
func (c *AccountController) UpdateDefault(ctx http.Context) http.Response {
	userID := requestctx.MustUserID(ctx)

	var req usersrequests.UpdateDefaultAccountRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	view, err := c.accounts.SetDefaultAccount(ctx.Context(), userID, req.Account())
	if err != nil {
		return mapError(ctx, err, "failed to update default account")
	}

	return ctx.Response().Success().Json(accountresources.NewDefaultAccount(view))
}
