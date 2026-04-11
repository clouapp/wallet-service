package config

import (
	"github.com/goravel/framework/facades"
	"github.com/spf13/cast"
)

// envString reads an environment-backed value via Goravel config (viper + OS env).
func envString(key, defaultValue string) string {
	v := facades.Config().Env(key, defaultValue)
	s := cast.ToString(v)
	if s == "" {
		return defaultValue
	}
	return s
}

func envInt(key string, defaultValue int) int {
	v := facades.Config().Env(key)
	if cast.ToString(v) == "" {
		return defaultValue
	}
	return cast.ToInt(v)
}

func envBool(key string, defaultValue bool) bool {
	v := facades.Config().Env(key)
	if cast.ToString(v) == "" {
		return defaultValue
	}
	return cast.ToBool(v)
}
