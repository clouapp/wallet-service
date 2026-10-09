package preferences

import (
	"errors"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

// mapError answers an error of the user service on the preference routes. A
// fiat code that is not an active fiat currency keeps its 400; anything else
// is logged and answered 500 with failure.
func mapError(ctx http.Context, err error, failure string) http.Response {
	if errors.Is(err, usersvc.ErrUnknownFiat) {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, usersvc.ErrUnknownFiat.Error())
	}
	slog.Error("controller internal error", "endpoint", failure, "error", err)
	return responses.FailMessage(ctx, http.StatusInternalServerError, failure)
}
