package currencies

import (
	"errors"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/requests"
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
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch currencies"})
	}
	return responses.Send(ctx, http.StatusOK, http.Json{"data": currencyresources.CurrenciesFrom(currencies)})
}

func (ctrl *CurrenciesController) GetCurrency(ctx http.Context) http.Response {
	var path requests.CurrencyCodeRequest
	path.Load(ctx)
	code := path.Code
	if code == "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "currency code is required"})
	}

	currency, err := ctrl.currencies.FindByCode(ctx.Context(), code)
	if errors.Is(err, models.ErrRepositoryNotFound) {
		currency, err = nil, nil
	}
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch currency"})
	}
	if currency == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "currency not found"})
	}
	return responses.Send(ctx, http.StatusOK, currencyresources.CurrencyPtr(currency))
}

func (ctrl *CurrenciesController) ConvertCurrency(ctx http.Context) http.Response {
	var query requests.ConvertCurrencyRequest
	query.Load(ctx)
	from := query.From
	to := query.To
	amountStr := query.Amount

	if from == "" || to == "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "from, to, and amount are required"})
	}

	amount, err := numeric.Parse("amount", amountStr)
	if err != nil || !amount.IsPositive() {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "amount must be a positive number"})
	}

	result, err := ctrl.prices.Convert(ctx.Context(), from, to, amount)
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": err.Error()})
	}
	rate := result.DivRound(amount, price.ConversionScale)
	return responses.Send(ctx, http.StatusOK, http.Json{
		"from":   from,
		"to":     to,
		"amount": numeric.NewDecimal(amount),
		"result": numeric.NewDecimal(result),
		"rate":   numeric.NewDecimal(rate),
	})
}
