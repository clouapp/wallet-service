package users

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	resources "github.com/macrowallets/waas/app/http/resources/platform/users"
	"github.com/macrowallets/waas/app/http/responses"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

// defaultLimit matches the account activity list and the account member list:
// an omitted limit is 20 and an omitted offset is 0.
const defaultLimit = 20

// UserController is the platform user actions a platform admin may call.
// Membership suspension stays on the account member route.
type UserController struct {
	users *usersvc.Service
}

// NewUserController wires the platform user handlers.
func NewUserController(users *usersvc.Service) *UserController {
	if users == nil {
		panic("platform users controller: users service is required")
	}
	return &UserController{users: users}
}

// Index godoc
//
//	@Summary		List platform users
//	@Description	Newest created_at first. Permission users.view; a platform admin may call it. The row is id, email, full_name, status, suspended_at, and totp_enabled. Password hashes, TOTP secrets, recovery codes, and session material are omitted.
//	@Tags			Platform Users
//	@Security		BearerAuth
//	@Produce		json
//	@Param			limit	query		int	false	"Page size"
//	@Param			offset	query		int	false	"Rows to skip"
//	@Success		200		{object}	map[string]any
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		403		{object}	responses.ErrorBody
//	@Router			/platform/users [get]
func (c *UserController) Index(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	limit, offset := pagination.ParseParams(ctx, defaultLimit)

	rows, total, err := c.users.List(ctx.Context(), actorID, limit, offset)
	if err != nil {
		return mapError(ctx, err, "list platform users")
	}

	return ctx.Response().Success().Json(pagination.Response(resources.UsersFrom(rows), total, limit, offset))
}

// Suspend godoc
//
//	@Summary		Suspend a platform user
//	@Description	Sets users.suspended_at. Only a platform admin may call it. The user's next request is refused. The activity row has a null account id.
//	@Tags			Platform Users
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"User UUID"
//	@Success		200	{object}	resources.Suspension
//	@Failure		401	{object}	responses.ErrorBody
//	@Failure		403	{object}	responses.ErrorBody
//	@Failure		404	{object}	responses.ErrorBody
//	@Router			/platform/users/{id}/suspend [post]
func (c *UserController) Suspend(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	targetID, err := requests.RouteUUID(ctx, "id")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid user id")
	}

	suspension, err := c.users.Suspend(ctx.Context(), actorID, targetID)
	if err != nil {
		return mapError(ctx, err, "suspend platform user")
	}

	return ctx.Response().Success().Json(resources.NewSuspension(suspension.ID, suspension.SuspendedAt))
}

// Reactivate godoc
//
//	@Summary		Reactivate a platform user
//	@Description	Clears users.suspended_at. Only a platform admin may call it. Sessions revoked by the suspension stay revoked.
//	@Tags			Platform Users
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"User UUID"
//	@Success		200	{object}	resources.Suspension
//	@Failure		401	{object}	responses.ErrorBody
//	@Failure		403	{object}	responses.ErrorBody
//	@Failure		404	{object}	responses.ErrorBody
//	@Router			/platform/users/{id}/reactivate [post]
func (c *UserController) Reactivate(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	targetID, err := requests.RouteUUID(ctx, "id")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid user id")
	}

	suspension, err := c.users.Reactivate(ctx.Context(), actorID, targetID)
	if err != nil {
		return mapError(ctx, err, "reactivate platform user")
	}

	return ctx.Response().Success().Json(resources.NewSuspension(suspension.ID, suspension.SuspendedAt))
}

// RevokeSessions godoc
//
//	@Summary		Revoke a platform user's sessions
//	@Description	Moves users.sessions_revoked_at and revokes refresh tokens. Only a platform admin may call it. The activity row is user.sessions_revoked with a null account id. The user is not suspended.
//	@Tags			Platform Users
//	@Security		BearerAuth
//	@Param			id	path	string	true	"User UUID"
//	@Success		204	"No content"
//	@Failure		401	{object}	responses.ErrorBody
//	@Failure		403	{object}	responses.ErrorBody
//	@Failure		404	{object}	responses.ErrorBody
//	@Router			/platform/users/{id}/sessions/revoke [post]
func (c *UserController) RevokeSessions(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	targetID, err := requests.RouteUUID(ctx, "id")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid user id")
	}

	if err := c.users.RevokeSessions(ctx.Context(), actorID, targetID); err != nil {
		return mapError(ctx, err, "revoke platform user sessions")
	}

	return ctx.Response().NoContent()
}

// ResetMFA godoc
//
//	@Summary		Reset a platform user's TOTP
//	@Description	Disables TOTP and clears the secret and recovery codes. Permission users.mfa.reset; a platform admin may call it. The activity row is user.mfa_reset with a null account id. The user is not suspended. Their sessions are revoked.
//	@Tags			Platform Users
//	@Security		BearerAuth
//	@Param			id	path	string	true	"User UUID"
//	@Success		204	"No content"
//	@Failure		401	{object}	responses.ErrorBody
//	@Failure		403	{object}	responses.ErrorBody
//	@Failure		404	{object}	responses.ErrorBody
//	@Router			/platform/users/{id}/mfa [delete]
func (c *UserController) ResetMFA(ctx http.Context) http.Response {
	actorID := requestctx.MustUserID(ctx)

	targetID, err := requests.RouteUUID(ctx, "id")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid user id")
	}

	if err := c.users.ResetMFA(ctx.Context(), actorID, targetID); err != nil {
		return mapError(ctx, err, "reset platform user mfa")
	}

	return ctx.Response().NoContent()
}
