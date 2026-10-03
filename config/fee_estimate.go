package config

import "github.com/goravel/framework/facades"

// defaultFeeEstimateCacheTTLSeconds matches feeestimate.DefaultCacheTTL; 0 disables the cache.
const defaultFeeEstimateCacheTTLSeconds = 15

func registerFeeEstimate() {
	facades.Config().Add("fee_estimate", map[string]any{
		"cache_ttl_seconds": envInt("FEE_ESTIMATE_CACHE_TTL_SECONDS", defaultFeeEstimateCacheTTLSeconds),
	})
}
