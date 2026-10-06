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

		// Local HTTP mode: SIGINT/SIGTERM drain HTTP and stop the local workers within
		// this deadline. Keep it below the e2e starter's 20 s SIGTERM grace, after which
		// it sends SIGKILL.
		"shutdown_timeout_seconds": envInt("SHUTDOWN_TIMEOUT_SECONDS", 15),

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
			// Confirmations seeded on new chain rows (and their t-prefixed test
			// records). Base ~2 s blocks: 12 ≈ 24 s; Arbitrum ~0.25 s: 120 ≈ 30 s;
			// BSC ~0.45 s with fast finality: 30 ≈ 14 s; TRON 3 s blocks solidify after
			// 19 (2/3 of 27 SRs + 1): 20 ≈ 60 s; Litecoin ~2.5 min: 6 ≈ 15 min.
			"required_confirmations": map[string]any{
				"base":     envInt("BASE_REQUIRED_CONFIRMATIONS", 12),
				"arbitrum": envInt("ARBITRUM_REQUIRED_CONFIRMATIONS", 120),
				"bsc":      envInt("BSC_REQUIRED_CONFIRMATIONS", 30),
				"tron":     envInt("TRON_REQUIRED_CONFIRMATIONS", 20),
				"ltc":      envInt("LTC_REQUIRED_CONFIRMATIONS", 6),
			},
		},
		// TronGrid API key (TRON-PRO-API-KEY header). Optional: Nile, and light use of
		// mainnet, work without one at TronGrid's lower anonymous rate limit.
		"tron": map[string]any{
			"api_key": envString("TRON_API_KEY", ""),
		},
		// Secondary providers of the Bitcoin-family chains, tried in order when the
		// chain's RPC URL fails (reads) or does not decide on a broadcast. Comma-separated
		// Esplora / bitcoind JSON-RPC URLs, electrum+ssl://host:port?cert_sha256=HEX or a
		// Tatum gateway (https://*.tatum.io). Empty: the network's built-in list
		// (Litecoin mainnet and testnet; Bitcoin has none); "none": no fallback. The
		// optional API key is Tatum's (x-api-key, Tatum hosts only); with it the Data API
		// at tatum_data_api_url (default https://api.tatum.io) also serves UTXOs.
		"utxo_fallbacks": map[string]any{
			"btc":  utxoFallback("BTC"),
			"tbtc": utxoFallback("TBTC"),
			"ltc":  utxoFallback("LTC"),
			"tltc": utxoFallback("TLTC"),
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
			// Wallet balance read model refresh of every registered chain; 0 turns it off.
			// Spacing paces the wallets of one pass so public RPCs are not rate limited.
			"balance_refresh_interval_seconds": envInt("LOCAL_BALANCE_REFRESH_INTERVAL_SECONDS", 60),
			"balance_refresh_spacing_ms":       envInt("LOCAL_BALANCE_REFRESH_SPACING_MS", 500),
		},
		// Block window and parallelism of every deposit scan (Lambda and local); 0 keeps
		// the scanner defaults (50 blocks, 500 while catching up, 8 parallel fetches).
		"deposit_scan": map[string]any{
			"batch_blocks":    envInt("DEPOSIT_SCAN_BATCH_BLOCKS", 0),
			"catch_up_blocks": envInt("DEPOSIT_SCAN_CATCH_UP_BLOCKS", 0),
			"concurrency":     envInt("DEPOSIT_SCAN_CONCURRENCY", 0),
			// A block whose deposits fail to record is retried right away (3 retries at
			// 100/300/900 ms by default), then kept as pending in Redis and in an
			// append-only file under pending_dir (default ~/.local/state/macro-wallets/
			// deposit-pending) and retried from 30 s, doubling up to 30 min. 0 keeps a default.
			"retry_attempts":            envInt("DEPOSIT_SCAN_RETRY_ATTEMPTS", 0),
			"retry_delay_ms":            envInt("DEPOSIT_SCAN_RETRY_DELAY_MS", 0),
			"pending_retry_seconds":     envInt("DEPOSIT_PENDING_RETRY_SECONDS", 0),
			"pending_retry_max_seconds": envInt("DEPOSIT_PENDING_RETRY_MAX_SECONDS", 0),
			"max_new_pending_per_cycle": envInt("DEPOSIT_SCAN_MAX_NEW_PENDING_PER_CYCLE", 0),
			"pending_dir":               envString("DEPOSIT_PENDING_DIR", ""),
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

// defaultFeeEstimateCacheTTLSeconds matches feeestimate.DefaultCacheTTL; 0 disables the cache.
const defaultFeeEstimateCacheTTLSeconds = 15

func registerFeeEstimate() {
	facades.Config().Add("fee_estimate", map[string]any{
		"cache_ttl_seconds": envInt("FEE_ESTIMATE_CACHE_TTL_SECONDS", defaultFeeEstimateCacheTTLSeconds),
	})
}

// utxoFallback reads <PREFIX>_FALLBACK_RPC_URL (comma-separated, in order), the
// optional Tatum key <PREFIX>_FALLBACK_RPC_API_KEY and <PREFIX>_TATUM_DATA_API_URL.
func utxoFallback(prefix string) map[string]any {
	return map[string]any{
		"rpc_urls":           envString(prefix+"_FALLBACK_RPC_URL", ""),
		"api_key":            envString(prefix+"_FALLBACK_RPC_API_KEY", ""),
		"tatum_data_api_url": envString(prefix+"_TATUM_DATA_API_URL", ""),
	}
}
