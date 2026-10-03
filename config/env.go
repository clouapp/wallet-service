package config

import (
	"fmt"
	"strings"

	"github.com/goravel/framework/facades"
	"github.com/shopspring/decimal"
	"github.com/spf13/cast"

	"github.com/macrowallets/waas/pkg/numeric"
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

// envNonNegativeDecimal reads an exact decimal (no float rounding). A malformed or
// negative value is a configuration error and stops the process with the key name.
func envNonNegativeDecimal(key, defaultValue string) decimal.Decimal {
	text := strings.TrimSpace(cast.ToString(facades.Config().Env(key)))
	if text == "" {
		text = defaultValue
	}
	value, err := numeric.ParseNonNegative(key, text)
	if err != nil {
		panic(fmt.Sprintf("invalid configuration: %v", err))
	}
	return value
}
