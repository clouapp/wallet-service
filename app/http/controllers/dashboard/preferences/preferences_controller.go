package preferences

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	preferencesrequests "github.com/macrowallets/waas/app/http/requests/dashboard/preferences"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/currencies"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

// PreferencesController serves the dashboard preference routes.
type PreferencesController struct {
	users      *usersvc.Service
	currencies *currencies.Service
}

// PreferencesControllerDeps is everything the dashboard preferences controller needs.
// Every field is required.
type PreferencesControllerDeps struct {
	Users      *usersvc.Service
	Currencies *currencies.Service
}

// NewPreferencesController wires the dashboard preference handlers from PreferencesControllerDeps.
func NewPreferencesController(deps PreferencesControllerDeps) *PreferencesController {
	if deps.Users == nil {
		panic("dashboard preferences controller: users service is required")
	}
	if deps.Currencies == nil {
		panic("dashboard preferences controller: currencies service is required")
	}
	return &PreferencesController{
		users:      deps.Users,
		currencies: deps.Currencies,
	}
}

func (ctrl *PreferencesController) GetPreferences(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)
	prefs := user.Preferences
	if prefs == nil {
		prefs = &models.UserPreferences{}
	}
	return ctx.Response().Success().Json(http.Json{
		"preferred_fiat_code": prefs.GetPreferredFiat(),
		"display_in_fiat":     prefs.IsDisplayInFiat(),
	})
}

func (ctrl *PreferencesController) UpdatePreferences(ctx http.Context) http.Response {
	userID := requestctx.MustUserID(ctx)
	user := requestctx.MustUser(ctx)

	var req preferencesrequests.UpdatePreferencesRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}

	prefs := user.Preferences
	if prefs == nil {
		prefs = &models.UserPreferences{}
	}

	if req.PreferredFiatCode != "" {
		cur, err := ctrl.currencies.FindByCode(ctx.Context(), req.PreferredFiatCode)
		if err != nil || cur == nil || !cur.Active || cur.Type != models.CurrencyTypeFiat {
			return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid fiat currency code")
		}
		prefs.PreferredFiatCode = req.PreferredFiatCode
	}
	if req.DisplayInFiat != nil {
		prefs.DisplayInFiat = req.DisplayInFiat
	}

	if err := ctrl.users.UpdatePreferences(ctx.Context(), userID, prefs); err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to update preferences")
	}

	return ctx.Response().Success().Json(http.Json{
		"preferred_fiat_code": prefs.GetPreferredFiat(),
		"display_in_fiat":     prefs.IsDisplayInFiat(),
	})
}
