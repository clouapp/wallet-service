package controllers

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/wallet"
)

// AddressGenerationError answers a failed derivation. An upstream provider
// failure stays 502 provider_unavailable. A message the handler owns stays
// 422. Anything else is 500: the cause can name a key, a passphrase, or a
// query, so the log keeps the type and the body is internal error.
func AddressGenerationError(ctx http.Context, err error) http.Response {
	if err == nil {
		return nil
	}
	if upstreamProvider(err) {
		return responses.ProviderError(ctx, err)
	}
	for _, refusal := range []error{wallet.ErrWalletNotFound, wallet.ErrAddressPassphraseRequired, wallet.ErrInvalidPassphrase} {
		if errors.Is(err, refusal) {
			return responses.Error(ctx, http.StatusUnprocessableEntity, responses.CodeUnprocessable, refusal.Error())
		}
	}
	slog.Error("address generation failed", "error_type", fmt.Sprintf("%T", err))
	return responses.Error(ctx, http.StatusInternalServerError, responses.CodeInternal, "internal error")
}

// AddressUpdateError answers a failed address update. The messages the
// handler owns stay 404. A wrapped store failure is 500 without the query.
func AddressUpdateError(ctx http.Context, err error) http.Response {
	for _, refusal := range []error{wallet.ErrAddressNotFound, wallet.ErrAddressLabelNotString, wallet.ErrAddressExternalUserIDNotString} {
		if errors.Is(err, refusal) {
			return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, refusal.Error())
		}
	}
	slog.Error("address update failed", "error_type", fmt.Sprintf("%T", err))
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
