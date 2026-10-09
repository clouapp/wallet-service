package controllers

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletops"
)

// mapAddressError answers a failed address route. An upstream provider failure
// stays 502 provider_unavailable. A derivation refusal the wallet service owns
// is 422, an update refusal 404, an update that sets nothing 400, and a page of
// addresses that could not be read a plain 500 sentence. Anything else is 500:
// the cause can name a key, a passphrase, or a query, so the log keeps the type
// of the failed action and the body is internal error.
func mapAddressError(ctx http.Context, err error, action string) http.Response {
	if upstreamProvider(err) {
		return responses.ProviderError(ctx, err)
	}
	for _, refusal := range []error{wallet.ErrWalletNotFound, wallet.ErrAddressPassphraseRequired, wallet.ErrInvalidPassphrase} {
		if errors.Is(err, refusal) {
			return responses.Error(ctx, http.StatusUnprocessableEntity, responses.CodeUnprocessable, refusal.Error())
		}
	}
	for _, refusal := range []error{wallet.ErrAddressNotFound, wallet.ErrAddressLabelNotString, wallet.ErrAddressExternalUserIDNotString} {
		if errors.Is(err, refusal) {
			return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, refusal.Error())
		}
	}
	switch {
	case errors.Is(err, walletops.ErrNoFields):
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, walletops.ErrNoFields.Error())
	case errors.Is(err, walletops.ErrAddressesUnavailable):
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to fetch addresses")
	}
	slog.Error(action+" failed", "error_type", fmt.Sprintf("%T", err))
	return responses.Error(ctx, http.StatusInternalServerError, responses.CodeInternal, "internal error")
}

// upstreamProviderError is the method AWS API exceptions share. Handlers
// use it so the provider's own text is recognized without importing the SDK.
type upstreamProviderError interface {
	ErrorCode() string
}

func upstreamProvider(err error) bool {
	var api upstreamProviderError
	return errors.As(err, &api)
}
