package users

import (
	"errors"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/pagination"
	usersrequests "github.com/macrowallets/waas/app/http/requests/dashboard/users"
	"github.com/macrowallets/waas/app/http/responses"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// mapError answers an error on the caller's own routes (/v1/users/me). A
// sentinel, or a refusal of the list query, keeps the status and the words
// the route has always answered. Anything else is logged and answered 500
// with failure, the sentence naming what the handler was doing.
func mapError(ctx http.Context, err error, failure string) http.Response {
	switch {
	case errors.Is(err, pagination.ErrInvalidLimit):
		return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, pagination.ErrInvalidLimit.Error())
	case errors.Is(err, pagination.ErrInvalidOffset):
		return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, pagination.ErrInvalidOffset.Error())
	case errors.Is(err, usersrequests.ErrSearchTooLong):
		return responses.FailMessage(ctx, http.StatusBadRequest, usersrequests.ErrSearchTooLong.Error())
	case errors.Is(err, usersrequests.ErrUnknownEnvironment):
		return responses.FailMessage(ctx, http.StatusBadRequest, usersrequests.ErrUnknownEnvironment.Error())
	case errors.Is(err, accountsvc.ErrNotMember):
		return responses.Fail(ctx, http.StatusForbidden, responses.CodeForbidden, accountsvc.ErrNotMember.Error())
	case errors.Is(err, accountsvc.ErrUserNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, accountsvc.ErrUserNotFound.Error())
	default:
		slog.Error("controller internal error", "endpoint", failure, "error", err)
		return responses.FailMessage(ctx, http.StatusInternalServerError, failure)
	}
}
