package blockheight

import (
	"context"
	"errors"
	"fmt"

	"github.com/macrowallets/waas/app/models"
)

// providerChainIDByNetwork is the key under which the providers know each network's
// tip source: a chain id, or a dedicated key for networks no chain id names.
var providerChainIDByNetwork = map[string]string{
	models.NetworkEthereumMainnet: models.ChainETH,
	models.NetworkEthereumSepolia: models.ChainTETH,
	models.NetworkPolygonMainnet:  models.ChainPolygon,
	models.NetworkPolygonAmoy:     models.ChainTPolygon,
	models.NetworkBitcoinMainnet:  models.ChainBTC,
	models.NetworkBitcoinTestnet:  models.ChainTBTC,
	models.NetworkBitcoinTestnet4: TipSourceBitcoinTestnet4,
	models.NetworkSolanaMainnet:   models.ChainSOL,
	models.NetworkSolanaDevnet:    models.ChainTSOL,
}

// ErrTipFromChainRPC means no provider serves the network's tip: the caller reads
// the chain's own RPC instead (Etherscan's free tier does not cover Base or BSC).
var ErrTipFromChainRPC = errors.New("blockheight: network tip comes from the chain RPC")

var chainRPCTipNetworks = map[string]struct{}{
	models.NetworkBaseMainnet:     {},
	models.NetworkBaseSepolia:     {},
	models.NetworkArbitrumMainnet: {},
	models.NetworkArbitrumSepolia: {},
	models.NetworkBSCMainnet:      {},
	models.NetworkBSCTestnet:      {},
}

// NetworkRouted asks the inner provider for the tip of the network a chain record
// really points at, so a "polygon" record on Amoy reads the Amoy height instead of
// the Polygon mainnet one.
type NetworkRouted struct {
	inner          Provider
	networkByChain map[string]string
}

// RouteByNetwork wraps inner. networkByChain maps chain id to the network the
// record resolves to; chains missing from it keep their own id.
func RouteByNetwork(inner Provider, networkByChain map[string]string) *NetworkRouted {
	copied := make(map[string]string, len(networkByChain))
	for chainID, network := range networkByChain {
		copied[chainID] = network
	}
	return &NetworkRouted{inner: inner, networkByChain: copied}
}

func (p *NetworkRouted) GetBlockHeight(ctx context.Context, chainID string) (uint64, error) {
	if p == nil || p.inner == nil {
		return 0, fmt.Errorf("blockheight: no provider for chain %q", chainID)
	}
	network, known := p.networkByChain[chainID]
	if !known || network == "" {
		return p.inner.GetBlockHeight(ctx, chainID)
	}
	if _, fromChainRPC := chainRPCTipNetworks[network]; fromChainRPC {
		return 0, fmt.Errorf("%w: network %q of chain %q", ErrTipFromChainRPC, network, chainID)
	}
	providerChainID, routed := providerChainIDByNetwork[network]
	if !routed {
		return 0, fmt.Errorf("blockheight: no tip source for network %q of chain %q", network, chainID)
	}
	return p.inner.GetBlockHeight(ctx, providerChainID)
}
