package blockheight

import (
	"strings"

	"github.com/macrowallets/waas/app/models"
)

// NewProviders builds the tip provider of each adapter type, routed by the network
// each chain record resolves to (networkByChain: chain id → network). Etherscan
// rejects every call without an API key, so with a blank key EVM gets no provider and
// the confirmation tracker reads EVM tips from the chain's own RPC directly.
func NewProviders(etherscanAPIKey string, networkByChain map[string]string) map[string]Provider {
	providers := map[string]Provider{
		models.AdapterTypeBitcoin: RouteByNetwork(NewBitcoinProvider(), networkByChain),
		models.AdapterTypeSolana:  RouteByNetwork(NewSolanaPublicProvider(), networkByChain),
	}
	if key := strings.TrimSpace(etherscanAPIKey); key != "" {
		providers[models.AdapterTypeEVM] = RouteByNetwork(NewEtherscanProvider(key), networkByChain)
	}
	return providers
}
