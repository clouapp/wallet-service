package config

import "github.com/goravel/framework/facades"

func registerApp() {
	facades.Config().Add("app", map[string]any{
		"name":     envString("APP_NAME", "Macro Wallets"),
		"env":      envString("APP_ENV", "local"),
		"debug":    envBool("APP_DEBUG", false),
		"timezone": envString("APP_TIMEZONE", "UTC"),
		"locale":   envString("APP_LOCALE", "en"),
		"key":      envString("APP_KEY", ""),
		"url":      envString("APP_URL", "http://localhost"),
	})
}
