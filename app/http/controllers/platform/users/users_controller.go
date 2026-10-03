package users

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

// UsersController suspends and reactivates platform users. Only a platform
// admin may call it. Membership suspension stays on the account member route.
type UsersController struct {
	users *usersvc.Service
}

// NewUsersController wires the platform user handlers.
func NewUsersController(users *usersvc.Service) *UsersController {
	if users == nil {
		panic("platform users controller: users service is required")
	}
	return &UsersController{users: users}
}

// Suspend godoc
// @Summary      Suspend a platform user
// @Description  Sets users.suspended_at. Only a platform admin may call it. The user's next request is refused. The activity row has a null account id.
// @Tags         Platform Users
// @Security     BearerAuth
// @Produce      json
// @Param        id  path  string  true  "User UUID"
// @Success      200  {object}  suspensionBody
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /platform/users/{id}/suspend [post]
func (ctrl *UsersController) Suspend(ctx http.Context) http.Response {
	return ctrl.change(ctx, true)
}

// Reactivate godoc
// @Summary      Reactivate a platform user
// @Description  Clears users.suspended_at. Only a platform admin may call it. Sessions revoked by the suspension stay revoked.
// @Tags         Platform Users
// @Security     BearerAuth
// @Produce      json
// @Param        id  path  string  true  "User UUID"
// @Success      200  {object}  suspensionBody
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /platform/users/{id}/reactivate [post]
func (ctrl *UsersController) Reactivate(ctx http.Context) http.Response {
	return ctrl.change(ctx, false)
}

// RevokeSessions godoc
// @Summary      Revoke a platform user's sessions
// @Description  Moves users.sessions_revoked_at and revokes refresh tokens. Only a platform admin may call it. The activity row is user.sessions_revoked with a null account id. The user is not suspended.
// @Tags         Platform Users
// @Security     BearerAuth
// @Param        id  path  string  true  "User UUID"
// @Success      204  "No content"
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /platform/users/{id}/sessions/revoke [post]
func (ctrl *UsersController) RevokeSessions(ctx http.Context) http.Response {
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthorized"})
	}
	targetID, err := requests.RouteUUID(ctx, "id")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid user id"})
	}
	if err := ctrl.users.RevokeSessions(ctx.Context(), actorID, targetID); err != nil {
		if errResp := mapSuspensionError(ctx, err); errResp != nil {
			return errResp
		}
	}
	return ctx.Response().NoContent()
}

type suspensionBody struct {
	ID          uuid.UUID `json:"id"`
	SuspendedAt *string   `json:"suspended_at"`
}

func (ctrl *UsersController) change(ctx http.Context, suspend bool) http.Response {
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthorized"})
	}
	targetID, err := requests.RouteUUID(ctx, "id")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid user id"})
	}
	var (
		result  usersvc.Suspension
		callErr error
	)
	if suspend {
		result, callErr = ctrl.users.Suspend(ctx.Context(), actorID, targetID)
	} else {
		result, callErr = ctrl.users.Reactivate(ctx.Context(), actorID, targetID)
	}
	if errResp := mapSuspensionError(ctx, callErr); errResp != nil {
		return errResp
	}
	return responses.Send(ctx, http.StatusOK, suspensionBody{
		ID:          result.ID,
		SuspendedAt: formatSuspendedAt(result.SuspendedAt),
	})
}

func formatSuspendedAt(at *time.Time) *string {
	if at == nil || at.IsZero() {
		return nil
	}
	text := at.UTC().Format(time.RFC3339)
	return &text
}

func mapSuspensionError(ctx http.Context, err error) http.Response {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, usersvc.ErrPlatformForbidden), errors.Is(err, usersvc.ErrSessionsForbidden):
		return responses.Send(ctx, http.StatusForbidden, http.Json{"error": err.Error()})
	case errors.Is(err, usersvc.ErrNotFound):
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "user not found"})
	default:
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
	}
}
