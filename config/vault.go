package config

import "github.com/goravel/framework/facades"

// registerVault holds WaaS-specific settings (RPC, AWS, queues, provider API keys).
func registerVault() {
	cfg := facades.Config()
	cfg.Add("vault", map[string]any{
		"redis_url":          envString("REDIS_URL", ""),
		"lambda_mode":        envString("LAMBDA_MODE", ""),
		"port":               envString("PORT", "8080"),
		"api_key_secret":     envString("API_KEY_SECRET", ""),
		"master_key_ref":     envString("MASTER_KEY_REF", ""),
		"wallet_service_key": envString("WALLET_SERVICE_KEY", ""),
		"aws": map[string]any{
			"region":       envString("AWS_REGION", "us-east-1"),
			"endpoint_url": envString("AWS_ENDPOINT_URL", ""),
		},
		"rpc": map[string]any{
			"eth":     envString("ETH_RPC_URL", ""),
			"polygon": envString("POLYGON_RPC_URL", ""),
			"solana":  envString("SOLANA_RPC_URL", ""),
			"btc":     envString("BTC_RPC_URL", ""),
		},
		// "mainnet" or "testnet": the networks eth/btc/polygon/sol point at. Empty keeps
		// existing records as they are and seeds new ones as mainnet.
		"chains": map[string]any{
			"network_profile": envString("CHAIN_NETWORK_PROFILE", ""),
		},
		"queues": map[string]any{
			"webhook":    envString("WEBHOOK_QUEUE_URL", ""),
			"withdrawal": envString("WITHDRAWAL_QUEUE_URL", ""),
		},
		// Local HTTP mode only: stand-ins for the confirmation_tracker and
		// webhook_worker Lambdas. Delivery runs only when no webhook queue is configured.
		"local_workers": map[string]any{
			"enabled":                       envBool("LOCAL_WORKERS_ENABLED", true),
			"confirmation_interval_seconds": envInt("LOCAL_CONFIRMATION_INTERVAL_SECONDS", 30),
			"delivery_interval_seconds":     envInt("LOCAL_WEBHOOK_DELIVERY_INTERVAL_SECONDS", 5),
			// Comma-separated chain ids scanned for deposits locally (e.g. "sol"); empty
			// leaves deposit detection to the deposit_scanner Lambda.
			"deposit_scan_chains":           envString("LOCAL_DEPOSIT_SCAN_CHAINS", ""),
			"deposit_scan_interval_seconds": envInt("LOCAL_DEPOSIT_SCAN_INTERVAL_SECONDS", 5),
		},
		// Block window and parallelism of every deposit scan (Lambda and local); 0 keeps
		// the scanner defaults (50 blocks, 500 while catching up, 8 parallel fetches).
		"deposit_scan": map[string]any{
			"batch_blocks":    envInt("DEPOSIT_SCAN_BATCH_BLOCKS", 0),
			"catch_up_blocks": envInt("DEPOSIT_SCAN_CATCH_UP_BLOCKS", 0),
			"concurrency":     envInt("DEPOSIT_SCAN_CONCURRENCY", 0),
		},
		"webhooks": map[string]any{
			"alchemy_auth_token": envString("ALCHEMY_AUTH_TOKEN", ""),
			"helius_api_key":     envString("HELIUS_API_KEY", ""),
			"quicknode_api_key":  envString("QUICKNODE_API_KEY", ""),
			"etherscan_api_key":  envString("ETHERSCAN_API_KEY", ""),
		},
		"price": map[string]any{
			"coingecko_api_key":     envString("COINGECKO_API_KEY", ""),
			"coinmarketcap_api_key": envString("COINMARKETCAP_API_KEY", ""),
			"coinapi_key":           envString("COINAPI_KEY", ""),
		},
	})
}
