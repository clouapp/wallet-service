package currencies

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	currencyresources "github.com/macrowallets/waas/app/http/resources/dashboard/currencies"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/currencies"
	price "github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/pkg/numeric"
)

// CurrenciesController serves the dashboard currency and convert routes.
type CurrenciesController struct {
	currencies *currencies.Service
	prices     *price.Service
}

// CurrenciesControllerDeps is everything the dashboard currencies controller needs.
// Every field is required.
type CurrenciesControllerDeps struct {
	Currencies *currencies.Service
	Prices     *price.Service
}

// NewCurrenciesController wires the dashboard currency handlers from CurrenciesControllerDeps.
func NewCurrenciesController(deps CurrenciesControllerDeps) *CurrenciesController {
	if deps.Currencies == nil {
		panic("dashboard currencies controller: currencies service is required")
	}
	if deps.Prices == nil {
		panic("dashboard currencies controller: price service is required")
	}
	return &CurrenciesController{
		currencies: deps.Currencies,
		prices:     deps.Prices,
	}
}

func (ctrl *CurrenciesController) ListCurrencies(ctx http.Context) http.Response {
	currencies, err := ctrl.currencies.FindAllActive(ctx.Context())
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to fetch currencies")
	}
	return ctx.Response().Success().Json(http.Json{"data": currencyresources.CurrenciesFrom(currencies)})
}

func (ctrl *CurrenciesController) GetCurrency(ctx http.Context) http.Response {
	code := ctx.Request().Route("code")
	if code == "" {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "currency code is required")
	}

	currency, err := ctrl.currencies.FindByCode(ctx.Context(), code)
	if errors.Is(err, models.ErrRepositoryNotFound) {
		currency, err = nil, nil
	}
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to fetch currency")
	}
	if currency == nil {
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "currency not found")
	}
	return ctx.Response().Success().Json(currencyresources.CurrencyPtr(currency))
}

func (ctrl *CurrenciesController) ConvertCurrency(ctx http.Context) http.Response {
	from := ctx.Request().Query("from")
	to := ctx.Request().Query("to")
	amountStr := ctx.Request().Query("amount", "0")

	if from == "" || to == "" {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "from, to, and amount are required")
	}

	amount, err := numeric.Parse("amount", amountStr)
	if err != nil || !amount.IsPositive() {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "amount must be a positive number")
	}

	result, err := ctrl.prices.Convert(ctx.Context(), from, to, amount)
	if err != nil {
		return convertFailure(ctx, err)
	}
	rate := result.DivRound(amount, price.ConversionScale)
	return ctx.Response().Success().Json(http.Json{
		"from":   from,
		"to":     to,
		"amount": numeric.NewDecimal(amount),
		"result": numeric.NewDecimal(result),
		"rate":   numeric.NewDecimal(rate),
	})
}

// convertFailure keeps a missing currency at 400 without the code the customer
// typed. A missing quote is the provider. A store failure can carry SQL, so
// the log keeps the type.
func convertFailure(ctx http.Context, err error) http.Response {
	if errors.Is(err, price.ErrPriceNotQuoted) || errors.Is(err, price.ErrZeroPrice) {
		return responses.ProviderError(ctx, err)
	}
	if errors.Is(err, price.ErrCurrencyNotFound) {
		return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "currency not found")
	}
	if errors.Is(err, price.ErrCurrencyCodesRequired) || errors.Is(err, price.ErrCurrencyCodeRequired) {
		return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "from, to, and amount are required")
	}
	slog.Error("currency convert failed", "error_type", fmt.Sprintf("%T", err))
	return responses.Error(ctx, http.StatusInternalServerError, responses.CodeInternal, "internal error")
}
