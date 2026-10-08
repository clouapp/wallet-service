package config

import "github.com/goravel/framework/facades"

func registerApp() {
	appEnv := envString("APP_ENV", "local")
	facades.Config().Add("app", map[string]any{
		"name":     envString("APP_NAME", "Macro Wallets"),
		"env":      appEnv,
		"debug":    debugEnabled(appEnv, envBool("APP_DEBUG", false)),
		"timezone": envString("APP_TIMEZONE", "UTC"),
		"locale":   envString("APP_LOCALE", "en"),
		"key":      envString("APP_KEY", ""),
		"url":      envString("APP_URL", "http://localhost"),
	})
}

// debugEnabled honors APP_DEBUG only in the local environment. Production,
// staging, and every other environment boot with debug off.
func debugEnabled(appEnv string, appDebug bool) bool {
	return appEnv == "local" && appDebug
}
