package config

import (
	"os"
	"strings"
)

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

// NewSecurityConfig creates a new security configuration from environment variables
func NewSecurityConfig() *SecurityConfig {
	cfg := &SecurityConfig{}

	// Domain validation
	cfg.Domain.Enabled = getEnvBool("SECURITY_DOMAIN_ENABLED", true)
	cfg.Domain.AllowedDomains = getEnvList("SECURITY_ALLOWED_DOMAINS", []string{
		"localhost",
		"127.0.0.1",
		"*.cloubet.io",
		"*.cloubet.com",
	})

	// Header anomaly detection
	cfg.HeaderAnomaly.Enabled = getEnvBool("SECURITY_HEADER_ANOMALY_ENABLED", true)

	// Anti-XSS
	cfg.AntiXSS.Enabled = getEnvBool("SECURITY_ANTI_XSS_ENABLED", true)

	// Anti-SQL injection
	cfg.AntiSQLInjection.Enabled = getEnvBool("SECURITY_ANTI_SQL_INJECTION_ENABLED", true)

	// API Key
	cfg.APIKey.Secret = os.Getenv("API_KEY_SECRET")

	// Rate limiting
	cfg.RateLimit.Enabled = getEnvBool("SECURITY_RATE_LIMIT_ENABLED", true)
	cfg.RateLimit.RequestsPerMinute = getEnvInt("SECURITY_RATE_LIMIT_RPM", 1000)

	return cfg
}

// Helper functions
func getEnvBool(key string, defaultValue bool) bool {
	val := os.Getenv(key)
	if val == "" {
		return defaultValue
	}
	return val == "true" || val == "1"
}

func getEnvInt(key string, defaultValue int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultValue
	}
	// Simple conversion - could be improved
	switch val {
	case "0":
		return 0
	case "100":
		return 100
	case "500":
		return 500
	case "1000":
		return 1000
	case "5000":
		return 5000
	default:
		return defaultValue
	}
}

func getEnvList(key string, defaultValue []string) []string {
	val := os.Getenv(key)
	if val == "" {
		return defaultValue
	}
	// Split by comma and trim spaces
	parts := strings.Split(val, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
