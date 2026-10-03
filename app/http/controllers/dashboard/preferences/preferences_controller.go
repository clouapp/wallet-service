package preferences

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/currencies"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

func validateRequest(ctx http.Context, req http.FormRequest) http.Response {
	return controllers.ValidateRequest(ctx, req)
}

// PreferencesController serves the dashboard preference routes.
type PreferencesController struct {
	users      *usersvc.Service
	currencies *currencies.Service
}

func NewPreferencesController(
	users *usersvc.Service,
	currencies *currencies.Service,
) *PreferencesController {
	if users == nil {
		panic("dashboard preferences controller: users service is required")
	}
	if currencies == nil {
		panic("dashboard preferences controller: currencies service is required")
	}
	return &PreferencesController{
		users:      users,
		currencies: currencies,
	}
}

func (ctrl *PreferencesController) GetPreferences(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)
	prefs := user.Preferences
	if prefs == nil {
		prefs = &models.UserPreferences{}
	}
	return responses.Send(ctx, http.StatusOK, http.Json{
		"preferred_fiat_code": prefs.GetPreferredFiat(),
		"display_in_fiat":     prefs.IsDisplayInFiat(),
	})
}

func (ctrl *PreferencesController) UpdatePreferences(ctx http.Context) http.Response {
	userID := requestctx.MustUserID(ctx)
	user := requestctx.MustUser(ctx)

	var req requests.UpdatePreferencesRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	prefs := user.Preferences
	if prefs == nil {
		prefs = &models.UserPreferences{}
	}

	if req.PreferredFiatCode != "" {
		cur, err := ctrl.currencies.FindByCode(ctx.Context(), req.PreferredFiatCode)
		if err != nil || cur == nil || !cur.Active || cur.Type != models.CurrencyTypeFiat {
			return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid fiat currency code"})
		}
		prefs.PreferredFiatCode = req.PreferredFiatCode
	}
	if req.DisplayInFiat != nil {
		prefs.DisplayInFiat = req.DisplayInFiat
	}

	if err := ctrl.users.UpdatePreferences(ctx.Context(), userID, prefs); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to update preferences"})
	}

	return responses.Send(ctx, http.StatusOK, http.Json{
		"preferred_fiat_code": prefs.GetPreferredFiat(),
		"display_in_fiat":     prefs.IsDisplayInFiat(),
	})
}
