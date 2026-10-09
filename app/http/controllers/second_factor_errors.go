package controllers

import (
	"errors"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

// MapSecondFactorError answers a refused second factor: an invalid or spent
// login challenge, a wrong or replayed code, or too many attempts. It returns
// nil for any other error, which the caller maps.
func MapSecondFactorError(ctx http.Context, err error) http.Response {
	switch {
	case errors.Is(err, authsvc.ErrChallengeInvalid):
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid or expired partial token")
	case errors.Is(err, authsvc.ErrInvalidSecondFactor):
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid 2FA code")
	case errors.Is(err, authsvc.ErrSecondFactorLocked):
		return responses.Fail(ctx, http.StatusTooManyRequests, responses.CodeTooManyRequests, "too many 2FA attempts, sign in again later")
	default:
		return nil
	}
}
