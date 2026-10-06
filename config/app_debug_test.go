package config

import "testing"

func TestDebugEnabledOnlyInLocal(t *testing.T) {
	cases := []struct {
		name     string
		env      string
		appDebug bool
		want     bool
	}{
		{"local honors APP_DEBUG true", "local", true, true},
		{"local honors APP_DEBUG false", "local", false, false},
		{"production ignores APP_DEBUG true", "production", true, false},
		{"staging ignores APP_DEBUG true", "staging", true, false},
		{"testing ignores APP_DEBUG true", "testing", true, false},
		{"prod ignores APP_DEBUG true", "prod", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := debugEnabled(tc.env, tc.appDebug); got != tc.want {
				t.Fatalf("debugEnabled(%q, %v) = %v, want %v", tc.env, tc.appDebug, got, tc.want)
			}
		})
	}
}
