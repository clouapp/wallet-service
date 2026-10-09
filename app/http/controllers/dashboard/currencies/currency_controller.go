package currencies

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/requests"
	currencyresources "github.com/macrowallets/waas/app/http/resources/dashboard/currencies"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/currencies"
	price "github.com/macrowallets/waas/app/services/price"
)

// CurrencyController serves the dashboard currency and convert routes.
type CurrencyController struct {
	currencies *currencies.Service
	prices     *price.Service
}

// NewCurrencyController wires the controller with the currency catalogue and
// the price service.
func NewCurrencyController(currencies *currencies.Service, prices *price.Service) *CurrencyController {
	if currencies == nil {
		panic("dashboard currencies controller: currencies service is required")
	}
	if prices == nil {
		panic("dashboard currencies controller: price service is required")
	}
	return &CurrencyController{currencies: currencies, prices: prices}
}

// Index lists the active currencies.
func (c *CurrencyController) Index(ctx http.Context) http.Response {
	currencies, err := c.currencies.FindAllActive(ctx.Context())
	if err != nil {
		return mapError(ctx, err, "fetch currencies")
	}

	return ctx.Response().Success().Json(http.Json{"data": currencyresources.CurrenciesFrom(currencies)})
}

// Show returns one currency by code.
func (c *CurrencyController) Show(ctx http.Context) http.Response {
	code, err := requests.RouteParam(ctx, "code")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "currency code is required")
	}

	currency, err := c.currencies.Get(ctx.Context(), code)
	if err != nil {
		return mapError(ctx, err, "fetch currency")
	}

	return ctx.Response().Success().Json(currencyresources.CurrencyPtr(currency))
}

// Convert converts an amount between two currencies.
func (c *CurrencyController) Convert(ctx http.Context) http.Response {
	quote, err := c.prices.Quote(ctx.Context(), price.QuoteInput{
		From:   ctx.Request().Query("from"),
		To:     ctx.Request().Query("to"),
		Amount: ctx.Request().Query("amount"),
	})
	if err != nil {
		return mapError(ctx, err, "convert currency")
	}

	return ctx.Response().Success().Json(currencyresources.NewConversion(quote))
}
