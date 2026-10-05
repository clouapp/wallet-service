package blockheight

import (
	"context"

	"github.com/macrowallets/waas/app/models"
)

// EtherscanKey returns the Etherscan API key at the moment a height is read.
// A blank result sends that call to the chain RPC instead of Etherscan.
type EtherscanKey func(ctx context.Context) string

// ProvidersDeps is the tip-provider set. A nil Key leaves EVM without a provider.
// NetworkByChain maps a chain id to the network each chain record resolves to.
// Blockstream reads Bitcoin mainnet and testnet3; a nil leaves those keys
// unconfigured. Testnet4 reads the Bitcoin testnet4 tip; a nil leaves that key
// unconfigured.
type ProvidersDeps struct {
	Key            EtherscanKey
	NetworkByChain map[string]string
	Blockstream    Provider
	Testnet4       Provider
}

// NewProviders builds the tip provider of each adapter type, routed by the network
// each chain record resolves to. The Etherscan key is not captured here: Key is
// called on each height read. A nil Key leaves EVM without a provider, and a blank
// result from Key does the same for that call, so the confirmation tracker reads
// the chain RPC directly.
func NewProviders(deps ProvidersDeps) map[string]Provider {
	providers := map[string]Provider{
		models.AdapterTypeBitcoin: RouteByNetwork(NewBitcoinProvider(BitcoinDeps{
			Blockstream: deps.Blockstream,
			Testnet4:    deps.Testnet4,
		}), deps.NetworkByChain),
		models.AdapterTypeSolana: RouteByNetwork(NewSolanaPublicProvider(), deps.NetworkByChain),
	}
	if deps.Key != nil {
		provider := NewEtherscanProvider(EtherscanDeps{KeyAtUse: deps.Key})
		providers[models.AdapterTypeEVM] = RouteByNetwork(provider, deps.NetworkByChain)
	}
	return providers
}
