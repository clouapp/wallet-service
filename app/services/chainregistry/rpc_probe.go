package chainregistry

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
)

const (
	evmChainIDMethod = "eth_chainId"
	hexPrefix        = "0x"

	// NetworkBitcoinSignet is a Bitcoin network no chain record targets; probing
	// it still has to fail the check rather than read as "unknown".
	NetworkBitcoinSignet = "bitcoin-signet"
	// NetworkTronShasta is a TRON testnet no chain record targets.
	NetworkTronShasta = "tron-shasta"
)

// Esplora REST hosts (Blockstream, mempool.space) put the network in the path.
var esploraHosts = []string{"blockstream.info", "mempool.space"}

// Litecoin Esplora hosts (litecoinspace.org/testnet/api) do the same.
var litecoinEsploraHosts = []string{"litecoinspace.org"}

var esploraNetworkByPathSegment = map[string]string{
	"testnet":  models.NetworkBitcoinTestnet,
	"testnet4": models.NetworkBitcoinTestnet4,
	"signet":   NetworkBitcoinSignet,
}

const litecoinTestnetPathSegment = "testnet"

// TronGrid hosts name the network in a host label (nile.trongrid.io,
// api.shasta.trongrid.io); api.trongrid.io is mainnet.
var tronNetworkByHostLabel = map[string]string{
	"nile":   models.NetworkTronNile,
	"nileex": models.NetworkTronNile,
	"shasta": NetworkTronShasta,
}

var tronMainnetHosts = []string{"api.trongrid.io"}

// NetworkXRPLDevnet is the XRPL devnet. No chain record targets it; a URL that
// names it must not be read as the altnet testnet.
const NetworkXRPLDevnet = "xrpl-devnet"

var xrplMainnetHosts = []string{"s1.ripple.com", "s2.ripple.com", "xrplcluster.com", "xrpl.ws"}

// ProbeRPCNetwork names the network rpcURL serves: EVM by asking eth_chainId,
// Bitcoin and Litecoin by the Esplora path, Solana and TRON by the host. "" means
// it cannot tell.
func ProbeRPCNetwork(ctx context.Context, record models.Chain, rpcURL string) (string, error) {
	switch record.AdapterType {
	case models.AdapterTypeEVM:
		return probeEVM(ctx, rpcURL)
	case models.AdapterTypeBitcoin:
		if models.IsLitecoinChainID(record.ID) {
			return LitecoinNetworkOfRPCURL(rpcURL), nil
		}
		return BitcoinNetworkOfRPCURL(rpcURL), nil
	case models.AdapterTypeSolana:
		return models.SolanaNetworkOfRPCURL(rpcURL), nil
	case models.AdapterTypeTron:
		return TronNetworkOfRPCURL(rpcURL), nil
	case models.AdapterTypeXRP:
		return XRPLNetworkOfRPCURL(rpcURL), nil
	default:
		return "", nil
	}
}

func probeEVM(ctx context.Context, rpcURL string) (string, error) {
	client := chain.NewJSONRPCCaller(chain.JSONRPCDeps{URL: rpcURL})
	if client == nil {
		return "", fmt.Errorf("%s: json-rpc client is not linked", evmChainIDMethod)
	}
	var chainIDHex string
	if err := client.Call(ctx, evmChainIDMethod, &chainIDHex); err != nil {
		return "", fmt.Errorf("%s: %w", evmChainIDMethod, err)
	}
	networkID, err := strconv.ParseInt(strings.TrimPrefix(strings.ToLower(chainIDHex), hexPrefix), 16, 64)
	if err != nil {
		return "", fmt.Errorf("%s returned %q: %w", evmChainIDMethod, chainIDHex, err)
	}
	return models.EVMNetworkName(networkID), nil
}

// BitcoinNetworkOfRPCURL names the network of an Esplora REST URL
// (https://blockstream.info/testnet/api → bitcoin-testnet), or "" for other hosts.
func BitcoinNetworkOfRPCURL(rpcURL string) string {
	host, path, ok := rpcHostAndPath(rpcURL)
	if !ok || !hostMatches(host, esploraHosts) {
		return ""
	}
	for _, segment := range strings.Split(path, "/") {
		if network, ok := esploraNetworkByPathSegment[segment]; ok {
			return network
		}
	}
	return models.NetworkBitcoinMainnet
}

// LitecoinNetworkOfRPCURL names the network of a Litecoin Esplora REST URL
// (https://litecoinspace.org/testnet/api → litecoin-testnet), or "" for other hosts.
func LitecoinNetworkOfRPCURL(rpcURL string) string {
	host, path, ok := rpcHostAndPath(rpcURL)
	if !ok || !hostMatches(host, litecoinEsploraHosts) {
		return ""
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == litecoinTestnetPathSegment {
			return models.NetworkLitecoinTestnet
		}
	}
	return models.NetworkLitecoinMainnet
}

// TronNetworkOfRPCURL names the TRON network of a TronGrid-style URL, or "" when
// the host does not say.
func TronNetworkOfRPCURL(rpcURL string) string {
	host, _, ok := rpcHostAndPath(rpcURL)
	if !ok {
		return ""
	}
	labels := strings.FieldsFunc(host, func(r rune) bool { return r == '.' || r == '-' })
	for _, label := range labels {
		if network, ok := tronNetworkByHostLabel[label]; ok {
			return network
		}
	}
	if hostMatches(host, tronMainnetHosts) {
		return models.NetworkTronMainnet
	}
	return ""
}

// XRPLNetworkOfRPCURL names the XRP Ledger network of a rippled URL. altnet is
// the public testnet. A devnet host is reported separately so it is not treated
// as that testnet. "" means the host does not say.
func XRPLNetworkOfRPCURL(rpcURL string) string {
	host, _, ok := rpcHostAndPath(rpcURL)
	if !ok {
		return ""
	}
	labels := strings.FieldsFunc(host, func(r rune) bool { return r == '.' || r == '-' })
	for _, label := range labels {
		switch label {
		case "altnet":
			return models.NetworkXRPLTestnet
		case "devnet":
			return NetworkXRPLDevnet
		}
	}
	if hostMatches(host, xrplMainnetHosts) {
		return models.NetworkXRPLMainnet
	}
	return ""
}

func rpcHostAndPath(rpcURL string) (host, path string, ok bool) {
	parsed, err := url.Parse(strings.TrimSpace(rpcURL))
	if err != nil || parsed.Hostname() == "" {
		return "", "", false
	}
	return strings.ToLower(parsed.Hostname()), strings.ToLower(parsed.Path), true
}

func hostMatches(host string, known []string) bool {
	for _, candidate := range known {
		if host == candidate || strings.HasSuffix(host, "."+candidate) {
			return true
		}
	}
	return false
}
