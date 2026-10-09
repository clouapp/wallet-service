package preferences

import (
	"github.com/macrowallets/waas/app/models"
)

// Preferences is GET and PUT /v1/me/preferences: the user's display
// preferences with their defaults applied (USD, shown in fiat). The keys keep
// the order the answer has always had.
type Preferences struct {
	DisplayInFiat     bool   `json:"display_in_fiat"`
	PreferredFiatCode string `json:"preferred_fiat_code"`
}

// NewPreferences shapes a preferences document. Nil is every default.
func NewPreferences(prefs *models.UserPreferences) Preferences {
	return Preferences{
		DisplayInFiat:     prefs.IsDisplayInFiat(),
		PreferredFiatCode: prefs.GetPreferredFiat(),
	}
}
