package controllers

import (
	"errors"
	"strconv"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
)

func ListCurrencies(ctx http.Context) http.Response {
	currencies, err := container.MustMake[*repositories.CurrencyRepository]().FindAllActive(ctx.Context())
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch currencies"})
	}
	return ctx.Response().Json(http.StatusOK, http.Json{"data": currencies})
}

func GetCurrency(ctx http.Context) http.Response {
	code := ctx.Request().Route("code")
	if code == "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "currency code is required"})
	}

	currency, err := container.MustMake[*repositories.CurrencyRepository]().FindByCode(ctx.Context(), code)
	if errors.Is(err, models.ErrRepositoryNotFound) {
		currency, err = nil, nil
	}
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch currency"})
	}
	if currency == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "currency not found"})
	}
	return ctx.Response().Json(http.StatusOK, currency)
}

func ConvertCurrency(ctx http.Context) http.Response {
	from := ctx.Request().Query("from", "")
	to := ctx.Request().Query("to", "")
	amountStr := ctx.Request().Query("amount", "0")

	if from == "" || to == "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "from, to, and amount are required"})
	}

	amount, err := strconv.ParseFloat(amountStr, 64)
	if err != nil || amount <= 0 {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "amount must be a positive number"})
	}

	result, err := container.Get().PriceService.Convert(ctx.Request().Origin().Context(), from, to, amount)
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": err.Error()})
	}

	rate := result / amount
	return ctx.Response().Json(http.StatusOK, http.Json{
		"from":   from,
		"to":     to,
		"amount": amount,
		"result": result,
		"rate":   rate,
	})
}
