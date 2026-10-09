package preferences

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	preferencesrequests "github.com/macrowallets/waas/app/http/requests/dashboard/preferences"
	resources "github.com/macrowallets/waas/app/http/resources/dashboard/preferences"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

// PreferencesController reads and changes the signed-in user's display
// preferences.
type PreferencesController struct {
	users *usersvc.Service
}

// NewPreferencesController wires the preference handlers to the user service,
// which checks a fiat code against the currencies.
func NewPreferencesController(users *usersvc.Service) *PreferencesController {
	if users == nil {
		panic("dashboard preferences controller: users service is required")
	}
	return &PreferencesController{users: users}
}

// Show answers the signed-in user's preferences with their defaults applied.
func (c *PreferencesController) Show(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)

	return ctx.Response().Success().Json(resources.NewPreferences(user.Preferences))
}

// Update changes the preferred fiat currency, the fiat display, or both, and
// answers the stored preferences.
func (c *PreferencesController) Update(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)

	var req preferencesrequests.UpdatePreferencesRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	prefs, err := c.users.ChangePreferences(ctx.Context(), user, usersvc.PreferencesInput{
		PreferredFiatCode: req.PreferredFiatCode,
		DisplayInFiat:     req.DisplayInFiat,
	})
	if err != nil {
		return mapError(ctx, err, "failed to update preferences")
	}

	return ctx.Response().Success().Json(resources.NewPreferences(prefs))
}
