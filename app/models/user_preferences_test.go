package models

import "testing"

func TestUserPreferencesValueIsJSONText(t *testing.T) {
	displayInFiat := false
	cases := []struct {
		name  string
		prefs UserPreferences
		want  string
	}{
		{"empty", UserPreferences{}, "{}"},
		{"set", UserPreferences{PreferredFiatCode: "BRL", DisplayInFiat: &displayInFiat}, `{"preferred_fiat_code":"BRL","display_in_fiat":false}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, err := tc.prefs.Value()
			if err != nil {
				t.Fatalf("Value() error = %v", err)
			}
			text, isText := value.(string)
			if !isText {
				t.Fatalf("Value() = %T, want string (a []byte reaches jsonb in binary form)", value)
			}
			if text != tc.want {
				t.Fatalf("Value() = %s, want %s", text, tc.want)
			}

			var scanned UserPreferences
			if err := scanned.Scan(text); err != nil {
				t.Fatalf("Scan(Value()) error = %v", err)
			}
			if scanned.GetPreferredFiat() != tc.prefs.GetPreferredFiat() || scanned.IsDisplayInFiat() != tc.prefs.IsDisplayInFiat() {
				t.Fatalf("Scan(Value()) = %+v, want %+v", scanned, tc.prefs)
			}
		})
	}
}
