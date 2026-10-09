package accounts

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	resources "github.com/macrowallets/waas/app/http/resources/platform/accounts"
	"github.com/macrowallets/waas/app/http/responses"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// UserController is GET /v1/platform/accounts/{accountId}/users.
// S3.4.1 names the route. A platform_admins row is the gate.
type UserController struct {
	accounts *accountsvc.Service
}

// NewUserController wires the platform account users list.
func NewUserController(accounts *accountsvc.Service) *UserController {
	if accounts == nil {
		panic("platform account users: account service is required")
	}
	return &UserController{accounts: accounts}
}

// Index godoc
//
//	@Summary		List an account's users
//	@Description	Newest user created_at first, then user id descending. A platform admin may call it. The row is the account member list plus the platform user fields id, email, full_name, status, suspended_at, and totp_enabled. Password hashes, TOTP secrets, recovery codes, and token material are omitted. An unknown account is 404 for a platform admin.
//	@Tags			Platform Accounts
//	@Security		BearerAuth
//	@Produce		json
//	@Param			accountId	path		string	true	"Account UUID"
//	@Param			limit		query		int		false	"Page size"
//	@Param			offset		query		int		false	"Rows to skip"
//	@Success		200			{object}	map[string]any
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		401			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/platform/accounts/{accountId}/users [get]
func (c *UserController) Index(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	accountID, err := requests.RouteUUID(ctx, "accountId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid account id")
	}
	limit, offset := pagination.ParseParams(ctx, defaultLimit)

	rows, total, err := c.accounts.ListUsersForPlatform(ctx.Context(), actorID, accountID, limit, offset)
	if err != nil {
		return mapError(ctx, err, "list platform account users")
	}

	return ctx.Response().Success().Json(pagination.Response(resources.AccountUsersFrom(rows), total, limit, offset))
}
