package currencies

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	currenciessvc "github.com/macrowallets/waas/app/services/currencies"
	"github.com/macrowallets/waas/app/services/price"
)

// mapError answers a currency service failure. A missing currency is 404, a
// refused conversion 400 and a missing quote the provider's 502. A conversion
// store failure can carry SQL, so its log keeps the type and the body is the
// internal error. Any other failure is 500 "failed to <action>", with the cause
// logged and kept out of the body.
func mapError(ctx http.Context, err error, action string) http.Response {
	switch {
	case errors.Is(err, currenciessvc.ErrNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "currency not found")
	case errors.Is(err, price.ErrQuoteFieldsRequired):
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, price.ErrQuoteFieldsRequired.Error())
	case errors.Is(err, price.ErrQuoteAmountInvalid):
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, price.ErrQuoteAmountInvalid.Error())
	case errors.Is(err, price.ErrPriceNotQuoted), errors.Is(err, price.ErrZeroPrice):
		return responses.ProviderError(ctx, err)
	case errors.Is(err, price.ErrCurrencyNotFound):
		return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "currency not found")
	case errors.Is(err, price.ErrCurrencyCodesRequired), errors.Is(err, price.ErrCurrencyCodeRequired):
		return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, price.ErrQuoteFieldsRequired.Error())
	case errors.Is(err, price.ErrQuoteFailed):
		slog.Error("currency convert failed", "error_type", fmt.Sprintf("%T", err))
		return responses.Error(ctx, http.StatusInternalServerError, responses.CodeInternal, "internal error")
	}
	slog.Error("currencies: "+action, "error", err)
	return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to "+action)
}
