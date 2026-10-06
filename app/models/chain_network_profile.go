package models

import "fmt"

// Chain network profiles: which networks the primary chain records (eth, btc,
// polygon, sol, base, arbitrum, bsc, tron, ltc, xrp) point at. The t-prefixed records
// are always test networks.
const (
	ChainNetworkProfileMainnet = "mainnet"
	ChainNetworkProfileTestnet = "testnet"
)

// ChainNetworkSpec is the network a primary chain record points at under a profile.
// NetworkID is the EVM id it signs with (nil for Bitcoin and Solana). Network is
// what the record's flags resolve to without an RPC URL; CompatibleNetworks are
// other networks the same flags serve when the RPC URL selects them (Bitcoin
// testnet4 shares tb1 addresses and is_testnet with testnet3).
type ChainNetworkSpec struct {
	Network            string
	CompatibleNetworks []string
	NetworkID          *int64
	IsTestnet          bool
}

// Accepts reports whether a record resolving to network satisfies the spec.
func (s ChainNetworkSpec) Accepts(network string) bool {
	if network == "" {
		return false
	}
	if network == s.Network {
		return true
	}
	for _, compatible := range s.CompatibleNetworks {
		if network == compatible {
			return true
		}
	}
	return false
}

var primaryChainNetworks = map[string]map[string]ChainNetworkSpec{
	ChainNetworkProfileMainnet: {
		ChainETH:      {Network: NetworkEthereumMainnet, NetworkID: int64Ptr(EVMNetworkIDEthereumMainnet)},
		ChainPolygon:  {Network: NetworkPolygonMainnet, NetworkID: int64Ptr(EVMNetworkIDPolygonMainnet)},
		ChainBTC:      {Network: NetworkBitcoinMainnet},
		ChainSOL:      {Network: NetworkSolanaMainnet},
		ChainBase:     {Network: NetworkBaseMainnet, NetworkID: int64Ptr(EVMNetworkIDBaseMainnet)},
		ChainArbitrum: {Network: NetworkArbitrumMainnet, NetworkID: int64Ptr(EVMNetworkIDArbitrumMainnet)},
		ChainBSC:      {Network: NetworkBSCMainnet, NetworkID: int64Ptr(EVMNetworkIDBSCMainnet)},
		ChainTron:     {Network: NetworkTronMainnet},
		ChainLTC:      {Network: NetworkLitecoinMainnet},
		ChainXRP:      {Network: NetworkXRPLMainnet},
	},
	ChainNetworkProfileTestnet: {
		ChainETH:      {Network: NetworkEthereumSepolia, NetworkID: int64Ptr(EVMNetworkIDEthereumSepolia), IsTestnet: true},
		ChainPolygon:  {Network: NetworkPolygonAmoy, NetworkID: int64Ptr(EVMNetworkIDPolygonAmoy), IsTestnet: true},
		ChainBTC:      {Network: NetworkBitcoinTestnet, CompatibleNetworks: []string{NetworkBitcoinTestnet4}, IsTestnet: true},
		ChainSOL:      {Network: NetworkSolanaDevnet, IsTestnet: true},
		ChainBase:     {Network: NetworkBaseSepolia, NetworkID: int64Ptr(EVMNetworkIDBaseSepolia), IsTestnet: true},
		ChainArbitrum: {Network: NetworkArbitrumSepolia, NetworkID: int64Ptr(EVMNetworkIDArbitrumSepolia), IsTestnet: true},
		ChainBSC:      {Network: NetworkBSCTestnet, NetworkID: int64Ptr(EVMNetworkIDBSCTestnet), IsTestnet: true},
		ChainTron:     {Network: NetworkTronNile, IsTestnet: true},
		ChainLTC:      {Network: NetworkLitecoinTestnet, IsTestnet: true},
		ChainXRP:      {Network: NetworkXRPLTestnet, IsTestnet: true},
	},
}

// PrimaryChainIDs lists the records a profile decides, in display order.
var PrimaryChainIDs = []string{ChainETH, ChainBTC, ChainPolygon, ChainSOL, ChainBase, ChainArbitrum, ChainBSC, ChainTron, ChainLTC, ChainXRP}

var testChainIDs = map[string]struct{}{
	ChainTETH:      {},
	ChainTBTC:      {},
	ChainTPolygon:  {},
	ChainTSOL:      {},
	ChainTBase:     {},
	ChainTArbitrum: {},
	ChainTBSC:      {},
	ChainTTron:     {},
	ChainTLTC:      {},
	ChainTXRP:      {},
}

var evmChainIDs = map[string]struct{}{
	ChainETH:       {},
	ChainTETH:      {},
	ChainPolygon:   {},
	ChainTPolygon:  {},
	ChainBase:      {},
	ChainTBase:     {},
	ChainArbitrum:  {},
	ChainTArbitrum: {},
	ChainBSC:       {},
	ChainTBSC:      {},
}

// IsEVMChainID reports whether chainID is a known EVM chain record (0x addresses
// derived from the secp256k1 key, the same on every EVM network).
func IsEVMChainID(chainID string) bool {
	_, ok := evmChainIDs[chainID]
	return ok
}

// IsTestChainID reports whether chainID is a t-prefixed record, on a test network
// under every profile.
func IsTestChainID(chainID string) bool {
	_, ok := testChainIDs[chainID]
	return ok
}

// IsChainNetworkProfile reports whether profile is a known profile name.
func IsChainNetworkProfile(profile string) bool {
	_, ok := primaryChainNetworks[profile]
	return ok
}

// PrimaryChainNetwork returns where chainID points under profile. ok is false for
// chains the profile does not decide (the t-prefixed records).
func PrimaryChainNetwork(profile, chainID string) (spec ChainNetworkSpec, ok bool, err error) {
	specs, known := primaryChainNetworks[profile]
	if !known {
		return ChainNetworkSpec{}, false, fmt.Errorf("unknown chain network profile %q (want %q or %q)",
			profile, ChainNetworkProfileMainnet, ChainNetworkProfileTestnet)
	}
	spec, ok = specs[chainID]
	spec.CompatibleNetworks = append([]string(nil), spec.CompatibleNetworks...)
	return spec, ok, nil
}

// EnvironmentForChainNetworkProfile is the account environment whose chain list
// holds the primary records under profile.
func EnvironmentForChainNetworkProfile(profile string) (string, error) {
	switch profile {
	case ChainNetworkProfileMainnet:
		return EnvironmentProd, nil
	case ChainNetworkProfileTestnet:
		return EnvironmentTest, nil
	default:
		return "", fmt.Errorf("unknown chain network profile %q", profile)
	}
}

func int64Ptr(v int64) *int64 { return &v }
