package config

import "github.com/goravel/framework/facades"

func registerAuth() {
	facades.Config().Add("auth", map[string]any{
		"defaults": map[string]any{"guard": "web"},
		"guards": map[string]any{
			"web": map[string]any{
				"driver":   "jwt",
				"provider": "users",
			},
		},
		// Second step of a password login for users with TOTP enabled.
		"two_factor": map[string]any{
			"challenge_ttl_seconds":  envInt("AUTH_2FA_CHALLENGE_TTL_SECONDS", 300),
			"max_attempts":           envInt("AUTH_2FA_MAX_ATTEMPTS", 5),
			"attempt_window_seconds": envInt("AUTH_2FA_ATTEMPT_WINDOW_SECONDS", 900),
		},
		"providers": map[string]any{
			"users": map[string]any{
				"driver": "orm",
			},
		},
	})
}
