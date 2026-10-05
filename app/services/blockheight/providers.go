package blockheight

import (
	"context"

	"github.com/macrowallets/waas/app/models"
)

// EtherscanKey returns the Etherscan API key at the moment a height is read.
// A blank result sends that call to the chain RPC instead of Etherscan.
type EtherscanKey func(ctx context.Context) string

// EtherscanDeps is everything the Etherscan block-height provider uses.
// A nil KeyAtUse keeps APIKey on every height read.
type EtherscanDeps struct {
	APIKey   string
	KeyAtUse EtherscanKey
}

// ProvidersDeps is the tip-provider set. A nil Key leaves EVM without a provider.
// NetworkByChain maps a chain id to the network each chain record resolves to.
// Etherscan reads EVM tips; a nil leaves that key unconfigured. Blockstream reads
// Bitcoin mainnet and testnet3; a nil leaves those keys unconfigured. Testnet4
// reads the Bitcoin testnet4 tip; a nil leaves that key unconfigured.
type ProvidersDeps struct {
	Key            EtherscanKey
	NetworkByChain map[string]string
	Etherscan      Provider
	Blockstream    Provider
	Testnet4       Provider
}

// NewProviders builds the tip provider of each adapter type, routed by the network
// each chain record resolves to. The Etherscan key is not read here: the Etherscan
// adapter calls it on each height read. A nil Key leaves EVM without a provider,
// and a blank result from that read does the same for that call, so the confirmation
// tracker reads the chain RPC directly.
func NewProviders(deps ProvidersDeps) map[string]Provider {
	providers := map[string]Provider{
		models.AdapterTypeBitcoin: RouteByNetwork(NewBitcoinProvider(BitcoinDeps{
			Blockstream: deps.Blockstream,
			Testnet4:    deps.Testnet4,
		}), deps.NetworkByChain),
		models.AdapterTypeSolana: RouteByNetwork(NewSolanaPublicProvider(), deps.NetworkByChain),
	}
	if deps.Key != nil {
		providers[models.AdapterTypeEVM] = RouteByNetwork(deps.Etherscan, deps.NetworkByChain)
	}
	return providers
}
