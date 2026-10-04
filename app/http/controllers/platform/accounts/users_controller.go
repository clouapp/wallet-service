package accounts

import (
	"errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// platformAccountUsersDefaultLimit matches GET /v1/platform/users: an omitted
// limit is 20 and an omitted offset is 0.
const platformAccountUsersDefaultLimit = 20

// UsersController is GET /v1/platform/accounts/{accountId}/users.
// S3.4.1 names the route. A platform_admins row is the gate.
type UsersController struct {
	accounts *accountsvc.Service
}

// NewUsersController wires the platform account users list.
func NewUsersController(accounts *accountsvc.Service) *UsersController {
	if accounts == nil {
		panic("platform account users: account service is required")
	}
	return &UsersController{accounts: accounts}
}

// Index godoc
// @Summary      List an account's users
// @Description  Newest user created_at first, then user id descending. A platform admin may call it. The row is the account member list plus the platform user fields id, email, full_name, status, suspended_at, and totp_enabled. Password hashes, TOTP secrets, recovery codes, and token material are omitted. An unknown account is 404 after the admin check.
// @Tags         Platform Accounts
// @Security     BearerAuth
// @Produce      json
// @Param        accountId  path   string  true   "Account UUID"
// @Param        limit      query  int     false  "Page size"
// @Param        offset     query  int     false  "Rows to skip"
// @Success      200  {object}  map[string]any
// @Failure      400  {object}  responses.ErrorBody
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /platform/accounts/{accountId}/users [get]
func (ctrl *UsersController) Index(ctx http.Context) http.Response {
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthorized"})
	}
	accountID, err := requests.RouteUUID(ctx, "accountId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid account id"})
	}
	limit, offset := pagination.ParseParams(ctx, platformAccountUsersDefaultLimit)
	rows, total, err := ctrl.accounts.ListUsersForPlatform(ctx.Context(), actorID, accountID, limit, offset)
	if errResp := mapAccountUsersError(ctx, err); errResp != nil {
		return errResp
	}
	return responses.Send(ctx, http.StatusOK, pagination.Response(platformAccountUserViews(rows), total, limit, offset))
}

func mapAccountUsersError(ctx http.Context, err error) http.Response {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, accountsvc.ErrPlatformAccountUsersForbidden):
		return responses.Send(ctx, http.StatusForbidden, http.Json{"error": err.Error()})
	case errors.Is(err, accountsvc.ErrAccountNotFound):
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": err.Error()})
	default:
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
	}
}
