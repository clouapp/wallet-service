package accounts

import (
	"errors"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	"github.com/macrowallets/waas/app/services/apitoken"
)

// mapError answers an error of the account services on the dashboard account
// routes. A sentinel keeps the status and the words the route has always
// answered. Anything else is logged and answered 500 with failure, the
// sentence naming what the handler was doing ("failed to create token").
func mapError(ctx http.Context, err error, failure string) http.Response {
	switch {
	case errors.Is(err, accountsvc.ErrAccessTokenNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "token not found")
	case errors.Is(err, apitoken.ErrSign):
		return internalError(ctx, err, "failed to sign token")
	default:
		return internalError(ctx, err, failure)
	}
}

// internalError logs err and answers 500 with failure. A sentence gets the
// internal code and a machine code (internal_error) is its own code, as the
// routes have always answered.
func internalError(ctx http.Context, err error, failure string) http.Response {
	slog.Error("controller internal error", "endpoint", failure, "error", err)
	return responses.FailMessage(ctx, http.StatusInternalServerError, failure)
}
