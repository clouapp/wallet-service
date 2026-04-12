package controllers

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/models"
)

func GetPreferences(ctx http.Context) http.Response {
	user := ctx.Value("user").(*models.User)
	prefs := user.Preferences
	if prefs == nil {
		prefs = &models.UserPreferences{}
	}
	return ctx.Response().Json(http.StatusOK, http.Json{
		"preferred_fiat_code": prefs.GetPreferredFiat(),
		"display_in_fiat":     prefs.IsDisplayInFiat(),
	})
}

func UpdatePreferences(ctx http.Context) http.Response {
	userID := ctx.Value("user_id").(uuid.UUID)
	user := ctx.Value("user").(*models.User)

	var req requests.UpdatePreferencesRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	prefs := user.Preferences
	if prefs == nil {
		prefs = &models.UserPreferences{}
	}

	if req.PreferredFiatCode != "" {
		if container.Get().CurrencyRepo != nil {
			cur, err := container.Get().CurrencyRepo.FindByCode(req.PreferredFiatCode)
			if err != nil || cur == nil || !cur.Active || cur.Type != models.CurrencyTypeFiat {
				return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid fiat currency code"})
			}
		}
		prefs.PreferredFiatCode = req.PreferredFiatCode
	}
	if req.DisplayInFiat != nil {
		prefs.DisplayInFiat = req.DisplayInFiat
	}

	if err := container.Get().UserRepo.UpdatePreferences(userID, prefs); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to update preferences"})
	}

	return ctx.Response().Json(http.StatusOK, http.Json{
		"preferred_fiat_code": prefs.GetPreferredFiat(),
		"display_in_fiat":     prefs.IsDisplayInFiat(),
	})
}
