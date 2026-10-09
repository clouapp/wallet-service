package wallets

import (
	"errors"
	"fmt"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletview"
)

// mapError answers a wallet service failure. A 4xx is a failure the caller can
// fix: an unknown chain, a passphrase that is too short, a missing account, or a
// wallet that is not theirs. A read that failed is 500 "failed to fetch <what>".
// Anything else is an outage: 500, with the cause logged and kept out of the
// body. One 409 for every error hid that outage behind a message about the
// caller.
func mapError(ctx http.Context, err error, action string) http.Response {
	var fetch *walletview.FetchError
	switch {
	case errors.Is(err, chainregistry.ErrUnknownChain):
		return responses.Fail(ctx, http.StatusConflict, responses.CodeConflict, "unknown chain")
	case errors.Is(err, wallet.ErrPassphraseTooShort):
		return responses.Fail(ctx, http.StatusUnprocessableEntity, responses.CodeUnprocessable, "passphrase must be at least 12 characters")
	case errors.Is(err, wallet.ErrAccountRequired), errors.Is(err, walletview.ErrAccountRequired):
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, accountRequiredMessage(err))
	case errors.Is(err, walletview.ErrWalletNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, walletview.ErrWalletNotFound.Error())
	case errors.As(err, &fetch):
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to fetch "+fetch.What)
	}
	return responses.InternalError(ctx, fmt.Errorf("%s: %w", action, err))
}

// accountRequiredMessage keeps the two sentences clients have always read: the
// wallet service's for a creation, the reads' for the others.
func accountRequiredMessage(err error) string {
	if errors.Is(err, wallet.ErrAccountRequired) {
		return wallet.ErrAccountRequired.Error()
	}
	return walletview.ErrAccountRequired.Error()
}
