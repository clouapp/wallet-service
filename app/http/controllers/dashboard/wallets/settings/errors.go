package settings

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	settingsrequests "github.com/macrowallets/waas/app/http/requests/dashboard/wallets/settings"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/walletsettings"
)

// mapError answers a wallet settings failure. A body that is missing, unreadable
// or changes nothing is 400, a rejected field 422, a fee change on a chain that
// cannot be read 422, and an archived wallet that is archived again 409. Any
// other failure is 500 "failed to <action>", with the cause logged and kept out
// of the body.
func mapError(ctx http.Context, err error, action string) http.Response {
	var field *walletsettings.FieldError
	switch {
	case errors.Is(err, settingsrequests.ErrBodyRequired):
		return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, settingsrequests.ErrBodyRequired.Error())
	case errors.Is(err, settingsrequests.ErrBodyUnreadable):
		return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, settingsrequests.ErrBodyUnreadable.Error())
	case errors.Is(err, walletsettings.ErrNoFields):
		return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, walletsettings.ErrNoFields.Error())
	case errors.As(err, &field):
		return responses.FieldsFailed(ctx, map[string][]string{field.Field: {field.Message}})
	case errors.Is(err, walletsettings.ErrChainNotFound):
		return responses.Fail(ctx, http.StatusUnprocessableEntity, responses.CodeUnprocessable, walletsettings.ErrChainNotFound.Error())
	case errors.Is(err, walletsettings.ErrAlreadyArchived):
		return responses.Fail(ctx, http.StatusConflict, responses.CodeConflict, walletsettings.ErrAlreadyArchived.Error())
	}

	slog.Error(action+" failed", "error_type", fmt.Sprintf("%T", err))
	return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to "+action)
}
