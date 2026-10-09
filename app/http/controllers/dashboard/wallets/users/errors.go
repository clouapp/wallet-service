package users

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// mapError answers a wallet membership failure. Roles outside the wallet
// vocabulary and a user who is not an active member of the account are 422, a
// user who is on the wallet already is 409, and a failed read of the existing
// membership is 503. Any other failure is 500
// "failed to <action>", with the cause logged and kept out of the body.
func mapError(ctx http.Context, err error, action string) http.Response {
	switch {
	case errors.Is(err, models.ErrInvalidWalletRoles):
		return responses.Error(ctx, http.StatusUnprocessableEntity, responses.CodeUnprocessable, models.ErrInvalidWalletRoles.Error())
	case errors.Is(err, walletrecords.ErrNotAccountMember):
		return responses.Fail(ctx, http.StatusUnprocessableEntity, responses.CodeUnprocessable, walletrecords.ErrNotAccountMember.Error())
	case errors.Is(err, walletrecords.ErrAlreadyWalletMember):
		return responses.Fail(ctx, http.StatusConflict, responses.CodeConflict, walletrecords.ErrAlreadyWalletMember.Error())
	case errors.Is(err, walletrecords.ErrMembershipLookup):
		return responses.Fail(ctx, http.StatusServiceUnavailable, responses.CodeUnavailable, walletrecords.ErrMembershipLookup.Error())
	case errors.Is(err, walletrecords.ErrMembershipRestore):
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, walletrecords.ErrMembershipRestore.Error())
	}

	slog.Error("wallet users: "+action, "error_type", fmt.Sprintf("%T", err))
	return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to "+action)
}
