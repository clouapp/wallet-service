package controllers

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
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
	if message, ok := exactClientMessage(err, "wallet not found", "passphrase is required for ed25519 address derivation", "invalid passphrase"); ok {
		return responses.Error(ctx, http.StatusUnprocessableEntity, responses.CodeUnprocessable, message)
	}
	slog.Error("address generation failed", "error_type", fmt.Sprintf("%T", err))
	return responses.Error(ctx, http.StatusInternalServerError, responses.CodeInternal, "internal error")
}

// AddressUpdateError answers a failed address update. The messages the
// handler owns stay 404. A wrapped store failure is 500 without the query.
func AddressUpdateError(ctx http.Context, err error) http.Response {
	if message, ok := exactClientMessage(err, "address not found", "update address: label must be a string", "update address: external_user_id must be a string"); ok {
		return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, message)
	}
	slog.Error("address update failed", "error_type", fmt.Sprintf("%T", err))
	return responses.Error(ctx, http.StatusInternalServerError, responses.CodeInternal, "internal error")
}

// exactClientMessage reports message when err is that sentence and nothing
// else. A wrap would carry the cause, so it does not match.
func exactClientMessage(err error, messages ...string) (string, bool) {
	if err == nil || errors.Unwrap(err) != nil {
		return "", false
	}
	text := err.Error()
	for _, message := range messages {
		if text == message {
			return message, true
		}
	}
	return "", false
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
