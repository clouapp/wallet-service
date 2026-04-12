package config

import (
	"strings"

	"github.com/goravel/framework/facades"
)

// SecurityConfig mirrors pkg/security expectations; loaded into config for future middleware use.
type SecurityConfig struct {
	Domain struct {
		Enabled        bool
		AllowedDomains []string
	}
	HeaderAnomaly struct {
		Enabled bool
	}
	AntiXSS struct {
		Enabled bool
	}
	AntiSQLInjection struct {
		Enabled bool
	}
	APIKey struct {
		Secret string
	}
	RateLimit struct {
		Enabled           bool
		RequestsPerMinute int
	}
}

func registerSecurity() {
	sec := SecurityConfig{}
	sec.Domain.Enabled = envBool("SECURITY_DOMAIN_ENABLED", true)
	sec.Domain.AllowedDomains = envStringList("SECURITY_ALLOWED_DOMAINS", []string{
		"localhost", "127.0.0.1", "*.cloubet.io", "*.cloubet.com",
	})
	sec.HeaderAnomaly.Enabled = envBool("SECURITY_HEADER_ANOMALY_ENABLED", true)
	sec.AntiXSS.Enabled = envBool("SECURITY_ANTI_XSS_ENABLED", true)
	sec.AntiSQLInjection.Enabled = envBool("SECURITY_ANTI_SQL_INJECTION_ENABLED", true)
	sec.APIKey.Secret = envString("API_KEY_SECRET", "")
	sec.RateLimit.Enabled = envBool("SECURITY_RATE_LIMIT_ENABLED", true)
	sec.RateLimit.RequestsPerMinute = envInt("SECURITY_RATE_LIMIT_RPM", 1000)

	facades.Config().Add("security", map[string]any{
		"config": sec,
	})
}

func envStringList(key string, defaultValue []string) []string {
	raw := envString(key, "")
	if raw == "" {
		return defaultValue
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
