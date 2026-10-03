package config

import (
	"strings"

	"github.com/goravel/framework/facades"
	"github.com/shopspring/decimal"
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

// SweepThresholds holds per-chain fallback thresholds consumed by the sweep service
// when the corresponding column in `chains` is NULL. Raw string values parse into
// math/big.Int at consumption; USD is an exact decimal for price-service comparison.
type SweepThresholds struct {
	GasReadinessRaw string
	DustNativeRaw   string
	DustUSD         decimal.Decimal
}

const (
	defaultDustUSDHigh = "1.00"
	defaultDustUSDLow  = "0.10"
)

// SweepDefaults returns the env-overridable default thresholds per chain.
// Values match the seeder in database/seeds/sweep_thresholds.go so behavior is
// identical whether the chain row has NULL thresholds or populated ones.
func SweepDefaults() map[string]SweepThresholds {
	return map[string]SweepThresholds{
		"eth": {
			GasReadinessRaw: envString("ETH_GAS_READINESS_THRESHOLD_WEI", "5000000000000000"),
			DustNativeRaw:   envString("ETH_DUST_THRESHOLD_NATIVE_WEI", "500000000000000"),
			DustUSD:         envNonNegativeDecimal("ETH_DUST_THRESHOLD_USD", defaultDustUSDHigh),
		},
		"teth": {
			GasReadinessRaw: envString("TETH_GAS_READINESS_THRESHOLD_WEI", "5000000000000000"),
			DustNativeRaw:   envString("TETH_DUST_THRESHOLD_NATIVE_WEI", "500000000000000"),
			DustUSD:         envNonNegativeDecimal("TETH_DUST_THRESHOLD_USD", defaultDustUSDHigh),
		},
		"polygon": {
			GasReadinessRaw: envString("POLYGON_GAS_READINESS_THRESHOLD_WEI", "500000000000000000"),
			DustNativeRaw:   envString("POLYGON_DUST_THRESHOLD_NATIVE_WEI", "100000000000000000"),
			DustUSD:         envNonNegativeDecimal("POLYGON_DUST_THRESHOLD_USD", defaultDustUSDLow),
		},
		"tpolygon": {
			GasReadinessRaw: envString("TPOLYGON_GAS_READINESS_THRESHOLD_WEI", "500000000000000000"),
			DustNativeRaw:   envString("TPOLYGON_DUST_THRESHOLD_NATIVE_WEI", "100000000000000000"),
			DustUSD:         envNonNegativeDecimal("TPOLYGON_DUST_THRESHOLD_USD", defaultDustUSDLow),
		},
		"sol": {
			GasReadinessRaw: envString("SOL_GAS_READINESS_THRESHOLD_LAMPORTS", "10000000"),
			DustNativeRaw:   envString("SOL_DUST_THRESHOLD_NATIVE_LAMPORTS", "1000000"),
			DustUSD:         envNonNegativeDecimal("SOL_DUST_THRESHOLD_USD", defaultDustUSDHigh),
		},
		"tsol": {
			GasReadinessRaw: envString("TSOL_GAS_READINESS_THRESHOLD_LAMPORTS", "10000000"),
			DustNativeRaw:   envString("TSOL_DUST_THRESHOLD_NATIVE_LAMPORTS", "1000000"),
			DustUSD:         envNonNegativeDecimal("TSOL_DUST_THRESHOLD_USD", defaultDustUSDHigh),
		},
		"base": {
			GasReadinessRaw: envString("BASE_GAS_READINESS_THRESHOLD_WEI", "200000000000000"),
			DustNativeRaw:   envString("BASE_DUST_THRESHOLD_NATIVE_WEI", "20000000000000"),
			DustUSD:         envNonNegativeDecimal("BASE_DUST_THRESHOLD_USD", defaultDustUSDLow),
		},
		"tbase": {
			GasReadinessRaw: envString("TBASE_GAS_READINESS_THRESHOLD_WEI", "200000000000000"),
			DustNativeRaw:   envString("TBASE_DUST_THRESHOLD_NATIVE_WEI", "20000000000000"),
			DustUSD:         envNonNegativeDecimal("TBASE_DUST_THRESHOLD_USD", defaultDustUSDLow),
		},
		"arbitrum": {
			GasReadinessRaw: envString("ARBITRUM_GAS_READINESS_THRESHOLD_WEI", "200000000000000"),
			DustNativeRaw:   envString("ARBITRUM_DUST_THRESHOLD_NATIVE_WEI", "20000000000000"),
			DustUSD:         envNonNegativeDecimal("ARBITRUM_DUST_THRESHOLD_USD", defaultDustUSDLow),
		},
		"tarbitrum": {
			GasReadinessRaw: envString("TARBITRUM_GAS_READINESS_THRESHOLD_WEI", "200000000000000"),
			DustNativeRaw:   envString("TARBITRUM_DUST_THRESHOLD_NATIVE_WEI", "20000000000000"),
			DustUSD:         envNonNegativeDecimal("TARBITRUM_DUST_THRESHOLD_USD", defaultDustUSDLow),
		},
		"bsc": {
			GasReadinessRaw: envString("BSC_GAS_READINESS_THRESHOLD_WEI", "500000000000000"),
			DustNativeRaw:   envString("BSC_DUST_THRESHOLD_NATIVE_WEI", "50000000000000"),
			DustUSD:         envNonNegativeDecimal("BSC_DUST_THRESHOLD_USD", defaultDustUSDLow),
		},
		"tbsc": {
			GasReadinessRaw: envString("TBSC_GAS_READINESS_THRESHOLD_WEI", "500000000000000"),
			DustNativeRaw:   envString("TBSC_DUST_THRESHOLD_NATIVE_WEI", "50000000000000"),
			DustUSD:         envNonNegativeDecimal("TBSC_DUST_THRESHOLD_USD", defaultDustUSDLow),
		},
		"btc": {
			GasReadinessRaw: "", // N/A: BTC fee comes from the input being spent
			DustNativeRaw:   envString("BTC_DUST_THRESHOLD_SATS", "10000"),
			DustUSD:         decimal.Zero, // N/A: no tokens on BTC
		},
		"tbtc": {
			GasReadinessRaw: "",
			DustNativeRaw:   envString("TBTC_DUST_THRESHOLD_SATS", "10000"),
			DustUSD:         decimal.Zero,
		},
	}
}
