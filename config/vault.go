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
		"queues": map[string]any{
			"webhook":    envString("WEBHOOK_QUEUE_URL", ""),
			"withdrawal": envString("WITHDRAWAL_QUEUE_URL", ""),
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
