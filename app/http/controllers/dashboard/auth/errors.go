package auth

import (
	"errors"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/responses"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

// mapError answers an error of the sign-in and credential flows on the
// /v1/auth routes. A refused second factor keeps its answer, and so does each
// sentinel, step by step. Anything else is logged and answered 500 with
// failure, the sentence naming what the handler was doing.
func mapError(ctx http.Context, err error, failure string) http.Response {
	if response := controllers.MapSecondFactorError(ctx, err); response != nil {
		return response
	}
	switch {
	case errors.Is(err, authsvc.ErrUserLookup):
		return responses.InternalError(ctx, err)
	case errors.Is(err, authsvc.ErrInvalidCredentials):
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, authsvc.ErrInvalidCredentials.Error())
	case errors.Is(err, authsvc.ErrUserInactive):
		return responses.Fail(ctx, http.StatusForbidden, responses.CodeForbidden, authsvc.ErrUserInactive.Error())
	case errors.Is(err, authsvc.ErrUserSuspended):
		return responses.SuspendedUser(ctx)
	case errors.Is(err, authsvc.ErrSecondFactorMissing):
		return responses.Fail(ctx, http.StatusUnprocessableEntity, responses.CodeUnprocessable, authsvc.ErrSecondFactorMissing.Error())
	case errors.Is(err, authsvc.ErrRefreshInvalid):
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, authsvc.ErrRefreshInvalid.Error())
	case errors.Is(err, authsvc.ErrResetTokenInvalid):
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, authsvc.ErrResetTokenInvalid.Error())
	case errors.Is(err, authsvc.ErrAccountsUnavailable):
		return responses.Fail(ctx, http.StatusServiceUnavailable, responses.CodeUnavailable, "failed to load accounts")
	case errors.Is(err, authsvc.ErrUserNotCreated):
		return internalError(ctx, err, "failed to create user")
	case errors.Is(err, authsvc.ErrSessionNotCreated):
		return internalError(ctx, err, "failed to create session")
	case errors.Is(err, authsvc.ErrPasswordNotHashed):
		return internalError(ctx, err, "failed to hash password")
	case errors.Is(err, authsvc.ErrPasswordNotSaved):
		return internalError(ctx, err, "failed to update password")
	case errors.Is(err, authsvc.ErrSessionsNotRevoked):
		return internalError(ctx, err, "password reset but existing sessions could not be revoked")
	default:
		return internalError(ctx, err, failure)
	}
}

// mapRefreshError is mapError for a refresh: a user who may not hold a session
// is a 401, not the 403 a login answers, because the refresh token is the
// credential that stopped working.
func mapRefreshError(ctx http.Context, err error) http.Response {
	if errors.Is(err, authsvc.ErrUserInactive) {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, authsvc.ErrUserInactive.Error())
	}
	return mapError(ctx, err, "failed to create session")
}

// internalError logs err and answers 500 with failure.
func internalError(ctx http.Context, err error, failure string) http.Response {
	slog.Error("controller internal error", "endpoint", failure, "error", err)
	return responses.FailMessage(ctx, http.StatusInternalServerError, failure)
}
