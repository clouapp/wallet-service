package controllers

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/pkg/numeric"
)

func ListCurrencies(ctx http.Context) http.Response {
	currencies, err := container.Get().CurrencyRepo.FindAllActive()
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch currencies"})
	}
	return ctx.Response().Json(http.StatusOK, http.Json{"data": currencies})
}

func GetCurrency(ctx http.Context) http.Response {
	code := ctx.Request().Route("code")
	if code == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "currency code is required"})
	}

	currency, err := container.Get().CurrencyRepo.FindByCode(code)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch currency"})
	}
	if currency == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "currency not found"})
	}
	return ctx.Response().Json(http.StatusOK, currency)
}

// ConvertCurrency answers amount, result and rate as JSON numbers with exact digits.
func ConvertCurrency(ctx http.Context) http.Response {
	from := ctx.Request().Query("from", "")
	to := ctx.Request().Query("to", "")
	amountStr := ctx.Request().Query("amount", "0")

	if from == "" || to == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "from, to, and amount are required"})
	}

	amount, err := numeric.Parse("amount", amountStr)
	if err != nil || !amount.IsPositive() {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "amount must be a positive number"})
	}

	result, err := container.Get().PriceService.Convert(ctx.Request().Origin().Context(), from, to, amount)
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": err.Error()})
	}

	rate := result.DivRound(amount, price.ConversionScale)
	return ctx.Response().Json(http.StatusOK, http.Json{
		"from":   from,
		"to":     to,
		"amount": numeric.NewDecimal(amount),
		"result": numeric.NewDecimal(result),
		"rate":   numeric.NewDecimal(rate),
	})
}
