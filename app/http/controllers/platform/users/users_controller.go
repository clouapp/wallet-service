package users

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	platformusers "github.com/macrowallets/waas/app/http/resources/platform/users"
	"github.com/macrowallets/waas/app/http/responses"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

// platformUsersDefaultLimit matches the account activity list and the account
// member list: an omitted limit is 20 and an omitted offset is 0.
const platformUsersDefaultLimit = 20

// UsersController is the platform user actions a platform admin may call.
// Membership suspension stays on the account member route.
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

// Index godoc
// @Summary      List platform users
// @Description  Newest created_at first. Permission users.view; a platform admin may call it. The row is id, email, full_name, status, suspended_at, and totp_enabled. Password hashes, TOTP secrets, recovery codes, and session material are omitted.
// @Tags         Platform Users
// @Security     BearerAuth
// @Produce      json
// @Param        limit   query  int  false  "Page size"
// @Param        offset  query  int  false  "Rows to skip"
// @Success      200  {object}  map[string]any
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Router       /platform/users [get]
func (ctrl *UsersController) Index(ctx http.Context) http.Response {
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "unauthorized")
	}
	limit, offset := pagination.ParseParams(ctx, platformUsersDefaultLimit)
	rows, total, err := ctrl.users.List(ctx.Context(), actorID, limit, offset)
	if errResp := mapListError(ctx, err); errResp != nil {
		return errResp
	}
	return responses.Send(ctx, http.StatusOK, pagination.Response(platformusers.UsersFrom(rows), total, limit, offset))
}

func mapListError(ctx http.Context, err error) http.Response {
	if err == nil {
		return nil
	}
	if errors.Is(err, usersvc.ErrViewForbidden) {
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, usersvc.ErrViewForbidden.Error())
	}
	return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternalError, "internal_error")
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
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "unauthorized")
	}
	targetID, err := requests.RouteUUID(ctx, "id")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid user id")
	}
	if err := ctrl.users.RevokeSessions(ctx.Context(), actorID, targetID); err != nil {
		if errResp := mapSuspensionError(ctx, err); errResp != nil {
			return errResp
		}
	}
	return ctx.Response().NoContent()
}

// ResetMFA godoc
// @Summary      Reset a platform user's TOTP
// @Description  Disables TOTP and clears the secret and recovery codes. Permission users.mfa.reset; a platform admin may call it. The activity row is user.mfa_reset with a null account id. The user is not suspended. Their sessions are revoked.
// @Tags         Platform Users
// @Security     BearerAuth
// @Param        id  path  string  true  "User UUID"
// @Success      204  "No content"
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /platform/users/{id}/mfa [delete]
func (ctrl *UsersController) ResetMFA(ctx http.Context) http.Response {
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "unauthorized")
	}
	targetID, err := requests.RouteUUID(ctx, "id")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid user id")
	}
	if err := ctrl.users.ResetMFA(ctx.Context(), actorID, targetID); err != nil {
		return mapMFAResetError(ctx, err)
	}
	return ctx.Response().NoContent()
}

func mapMFAResetError(ctx http.Context, err error) http.Response {
	switch {
	case errors.Is(err, usersvc.ErrNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "user not found")
	case errors.Is(err, usersvc.ErrMFAForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, usersvc.ErrMFAForbidden.Error())
	default:
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternalError, "internal_error")
	}
}

type suspensionBody struct {
	ID          uuid.UUID `json:"id"`
	SuspendedAt *string   `json:"suspended_at"`
}

func (ctrl *UsersController) change(ctx http.Context, suspend bool) http.Response {
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "unauthorized")
	}
	targetID, err := requests.RouteUUID(ctx, "id")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid user id")
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
	case errors.Is(err, usersvc.ErrPlatformForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, usersvc.ErrPlatformForbidden.Error())
	case errors.Is(err, usersvc.ErrSessionsForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, usersvc.ErrSessionsForbidden.Error())
	case errors.Is(err, usersvc.ErrNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "user not found")
	default:
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternalError, "internal_error")
	}
}
