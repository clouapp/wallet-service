package users

import (
	"errors"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/pagination"
	usersrequests "github.com/macrowallets/waas/app/http/requests/dashboard/users"
	"github.com/macrowallets/waas/app/http/responses"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
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
	case errors.Is(err, authsvc.ErrWrongPassword):
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, authsvc.ErrWrongPassword.Error())
	case errors.Is(err, authsvc.ErrPasswordNotHashed):
		return internalError(ctx, err, "failed to hash password")
	case errors.Is(err, authsvc.ErrPasswordNotSaved):
		return internalError(ctx, err, "failed to update password")
	case errors.Is(err, authsvc.ErrUserNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, authsvc.ErrUserNotFound.Error())
	case errors.Is(err, authsvc.ErrTOTPAlreadyEnabled):
		return responses.Fail(ctx, http.StatusConflict, responses.CodeConflict, authsvc.ErrTOTPAlreadyEnabled.Error())
	case errors.Is(err, authsvc.ErrTOTPNotStarted):
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, authsvc.ErrTOTPNotStarted.Error())
	case errors.Is(err, authsvc.ErrTOTPCodeInvalid):
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, authsvc.ErrTOTPCodeInvalid.Error())
	case errors.Is(err, authsvc.ErrTOTPNotGenerated):
		return internalError(ctx, err, "failed to generate TOTP secret")
	case errors.Is(err, authsvc.ErrTOTPNotSealed):
		return secretFailure(ctx, "user: setup totp: seal failed", "failed to encrypt secret")
	case errors.Is(err, authsvc.ErrTOTPNotSaved):
		return secretFailure(ctx, "user: setup totp: save failed", "failed to save TOTP secret")
	case errors.Is(err, authsvc.ErrTOTPNotOpened):
		return secretFailure(ctx, "user: confirm totp: open secret failed", "failed to decrypt secret")
	case errors.Is(err, authsvc.ErrTOTPNotEnabled):
		return internalError(ctx, err, "failed to enable 2FA")
	case errors.Is(err, authsvc.ErrRecoveryCodesNotGenerated):
		return internalError(ctx, err, "failed to generate recovery codes")
	case errors.Is(err, authsvc.ErrTOTPNotDisabled):
		return internalError(ctx, err, "failed to disable 2FA")
	default:
		return internalError(ctx, err, failure)
	}
}

// mapPasswordError is mapError for a password change: sessions that could not
// be renewed after the new password was stored say so.
func mapPasswordError(ctx http.Context, err error) http.Response {
	if errors.Is(err, authsvc.ErrSessionsNotReplaced) {
		return internalError(ctx, err, "password updated but sessions could not be renewed")
	}
	return mapError(ctx, err, "failed to update password")
}

// mapDisableError is mapError for turning 2FA off: a refused second factor
// keeps the auth answers, and sessions that could not be renewed after 2FA
// went off say so. Any other verifier failure is a 500 "internal error".
func mapDisableError(ctx http.Context, err error) http.Response {
	if response := controllers.MapSecondFactorError(ctx, err); response != nil {
		return response
	}
	if errors.Is(err, authsvc.ErrSessionsNotReplaced) {
		return internalError(ctx, err, "2FA disabled but sessions could not be renewed")
	}
	return mapError(ctx, err, "internal error")
}

// secretFailure answers a TOTP secret that could not be sealed, stored or
// opened. The log names the step and leaves the cause out, so nothing of the
// secret reaches it.
func secretFailure(ctx http.Context, step, failure string) http.Response {
	appfacades.Log().WithContext(ctx).Errorf("%s", step)
	return responses.FailMessage(ctx, http.StatusInternalServerError, failure)
}

// internalError logs err and answers 500 with failure. A sentence gets the
// internal code and a machine code (internal_error) is its own code, as the
// routes have always answered.
func internalError(ctx http.Context, err error, failure string) http.Response {
	slog.Error("controller internal error", "endpoint", failure, "error", err)
	return responses.FailMessage(ctx, http.StatusInternalServerError, failure)
}
