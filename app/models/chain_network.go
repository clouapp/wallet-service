package models

import (
	"net/url"
	"strings"
)

const (
	NetworkEthereumMainnet = "ethereum-mainnet"
	NetworkEthereumSepolia = "ethereum-sepolia"
	NetworkPolygonMainnet  = "polygon-mainnet"
	NetworkPolygonAmoy     = "polygon-amoy"
	NetworkBitcoinMainnet  = "bitcoin-mainnet"
	NetworkBitcoinTestnet  = "bitcoin-testnet"
	NetworkBitcoinTestnet4 = "bitcoin-testnet4"
	NetworkSolanaMainnet   = "solana-mainnet"
	NetworkSolanaDevnet    = "solana-devnet"
	NetworkSolanaTestnet   = "solana-testnet"
)

const (
	EVMNetworkIDEthereumMainnet int64 = 1
	EVMNetworkIDEthereumSepolia int64 = 11155111
	EVMNetworkIDPolygonMainnet  int64 = 137
	EVMNetworkIDPolygonAmoy     int64 = 80002
)

const (
	solanaClusterMainnet = "mainnet"
	solanaClusterDevnet  = "devnet"
	solanaClusterTestnet = "testnet"
)

const bitcoinTestnet4Label = "testnet4"

var testnetNetworks = map[string]struct{}{
	NetworkEthereumSepolia: {},
	NetworkPolygonAmoy:     {},
	NetworkBitcoinTestnet:  {},
	NetworkBitcoinTestnet4: {},
	NetworkSolanaDevnet:    {},
	NetworkSolanaTestnet:   {},
}

// ResolvedNetwork is where a chain record actually points. Name is "" when the
// record does not identify a known network.
type ResolvedNetwork struct {
	Name    string
	Testnet bool
}

// IsTestnetNetwork reports whether network is a known test network.
func IsTestnetNetwork(network string) bool {
	_, ok := testnetNetworks[network]
	return ok
}

// Network names the network a chain record points at, or "" when unknown. EVM
// chains are identified by network_id (the id they sign with), not by chain ID or
// is_testnet: a "polygon" record can be configured for Amoy.
func (c *Chain) Network() string {
	if c == nil {
		return ""
	}
	switch c.AdapterType {
	case AdapterTypeEVM:
		if c.NetworkID == nil {
			return ""
		}
		return EVMNetworkName(*c.NetworkID)
	case AdapterTypeBitcoin:
		if c.IsTestnet {
			return NetworkBitcoinTestnet
		}
		return NetworkBitcoinMainnet
	case AdapterTypeSolana:
		if c.IsTestnet {
			return NetworkSolanaDevnet
		}
		return NetworkSolanaMainnet
	default:
		return ""
	}
}

// ResolveNetwork classifies the record by the network it really uses: EVM by
// network_id, Bitcoin by is_testnet (it also selects tb1/bc1 addresses) refined to
// testnet4 when its resolved RPC URL names testnet4 (tb1 addresses are the same on
// both testnets), and Solana by the cluster its resolved RPC URL names, since Solana
// addresses carry no network and is_testnet only lists the chain in an account
// environment. rpcURL must be resolved (not an "env:NAME" reference). Records that
// name no known network fall back to is_testnet.
func (c *Chain) ResolveNetwork(rpcURL string) ResolvedNetwork {
	if c == nil {
		return ResolvedNetwork{}
	}
	name := c.Network()
	switch c.AdapterType {
	case AdapterTypeSolana:
		if cluster := SolanaNetworkOfRPCURL(rpcURL); cluster != "" {
			name = cluster
		}
	case AdapterTypeBitcoin:
		if c.IsTestnet && IsBitcoinTestnet4RPCURL(rpcURL) {
			name = NetworkBitcoinTestnet4
		}
	}
	if name == "" {
		return ResolvedNetwork{Testnet: c.IsTestnet}
	}
	return ResolvedNetwork{Name: name, Testnet: IsTestnetNetwork(name)}
}

// SolanaNetworkOfRPCURL names the Solana cluster an RPC URL points at when its
// host says so (api.devnet.solana.com, solana-mainnet.g.alchemy.com,
// devnet.helius-rpc.com, ...), or "" when it does not.
func SolanaNetworkOfRPCURL(rpcURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rpcURL))
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	labels := strings.FieldsFunc(strings.ToLower(parsed.Hostname()), func(r rune) bool {
		return r == '.' || r == '-'
	})
	for _, label := range labels {
		switch label {
		case solanaClusterDevnet:
			return NetworkSolanaDevnet
		case solanaClusterTestnet:
			return NetworkSolanaTestnet
		case solanaClusterMainnet:
			return NetworkSolanaMainnet
		}
	}
	return ""
}

// IsBitcoinTestnet4RPCURL reports whether a Bitcoin RPC URL names testnet4 in a
// path segment (https://mempool.space/testnet4/api) or a host label
// (testnet4.example.com, btc-testnet4.example.com).
func IsBitcoinTestnet4RPCURL(rpcURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rpcURL))
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	for _, segment := range strings.Split(strings.ToLower(parsed.Path), "/") {
		if segment == bitcoinTestnet4Label {
			return true
		}
	}
	hostLabels := strings.FieldsFunc(strings.ToLower(parsed.Hostname()), func(r rune) bool {
		return r == '.' || r == '-'
	})
	for _, label := range hostLabels {
		if label == bitcoinTestnet4Label {
			return true
		}
	}
	return false
}

// EVMNetworkName names the EVM network a chain id signs for, or "" when unknown.
func EVMNetworkName(networkID int64) string {
	switch networkID {
	case EVMNetworkIDEthereumMainnet:
		return NetworkEthereumMainnet
	case EVMNetworkIDEthereumSepolia:
		return NetworkEthereumSepolia
	case EVMNetworkIDPolygonMainnet:
		return NetworkPolygonMainnet
	case EVMNetworkIDPolygonAmoy:
		return NetworkPolygonAmoy
	default:
		return ""
	}
}
