package preferences_test

import (
	"encoding/json"
	"testing"

	"github.com/macrowallets/waas/app/http/resources/dashboard/preferences"
	"github.com/macrowallets/waas/app/models"
)

func TestPreferences_KeepTheMapWire(t *testing.T) {
	t.Parallel()

	off := false
	for name, prefs := range map[string]*models.UserPreferences{
		"none":     nil,
		"defaults": {},
		"set":      {PreferredFiatCode: "EUR", DisplayInFiat: &off},
	} {
		t.Run(name, func(t *testing.T) {
			legacy := prefs
			if legacy == nil {
				legacy = &models.UserPreferences{}
			}
			want, err := json.Marshal(map[string]any{
				"preferred_fiat_code": legacy.GetPreferredFiat(),
				"display_in_fiat":     legacy.IsDisplayInFiat(),
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(preferences.NewPreferences(prefs))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatalf("wire changed\n got %s\nwant %s", got, want)
			}
		})
	}
}
