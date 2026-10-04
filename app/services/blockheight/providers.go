package blockheight

import (
	"context"

	"github.com/macrowallets/waas/app/models"
)

// EtherscanKey returns the Etherscan API key at the moment a height is read.
// A blank result sends that call to the chain RPC instead of Etherscan.
type EtherscanKey func(ctx context.Context) string

// NewProviders builds the tip provider of each adapter type, routed by the network
// each chain record resolves to (networkByChain: chain id → network). The Etherscan
// key is not captured here: key is called on each height read. A nil key leaves EVM
// without a provider, and a blank result from key does the same for that call, so
// the confirmation tracker reads the chain RPC directly.
func NewProviders(key EtherscanKey, networkByChain map[string]string) map[string]Provider {
	providers := map[string]Provider{
		models.AdapterTypeBitcoin: RouteByNetwork(NewBitcoinProvider(), networkByChain),
		models.AdapterTypeSolana:  RouteByNetwork(NewSolanaPublicProvider(), networkByChain),
	}
	if key != nil {
		provider := NewEtherscanProvider("")
		provider.keyAtUse = key
		providers[models.AdapterTypeEVM] = RouteByNetwork(provider, networkByChain)
	}
	return providers
}
