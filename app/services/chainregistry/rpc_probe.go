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
)

// Esplora REST hosts (Blockstream, mempool.space) put the network in the path.
var esploraHosts = []string{"blockstream.info", "mempool.space"}

var esploraNetworkByPathSegment = map[string]string{
	"testnet":  models.NetworkBitcoinTestnet,
	"testnet4": models.NetworkBitcoinTestnet4,
	"signet":   NetworkBitcoinSignet,
}

// ProbeRPCNetwork names the network rpcURL serves: EVM by asking eth_chainId,
// Bitcoin by the Esplora path, Solana by the host. "" means it cannot tell.
func ProbeRPCNetwork(ctx context.Context, record models.Chain, rpcURL string) (string, error) {
	switch record.AdapterType {
	case models.AdapterTypeEVM:
		return probeEVM(ctx, rpcURL)
	case models.AdapterTypeBitcoin:
		return BitcoinNetworkOfRPCURL(rpcURL), nil
	case models.AdapterTypeSolana:
		return models.SolanaNetworkOfRPCURL(rpcURL), nil
	default:
		return "", nil
	}
}

func probeEVM(ctx context.Context, rpcURL string) (string, error) {
	var chainIDHex string
	if err := chain.NewRPCClient(chain.RPCClientDeps{URL: rpcURL}).Call(ctx, evmChainIDMethod, &chainIDHex); err != nil {
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
	parsed, err := url.Parse(strings.TrimSpace(rpcURL))
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	if !isEsploraHost(strings.ToLower(parsed.Hostname())) {
		return ""
	}
	for _, segment := range strings.Split(strings.ToLower(parsed.Path), "/") {
		if network, ok := esploraNetworkByPathSegment[segment]; ok {
			return network
		}
	}
	return models.NetworkBitcoinMainnet
}

func isEsploraHost(host string) bool {
	for _, known := range esploraHosts {
		if host == known || strings.HasSuffix(host, "."+known) {
			return true
		}
	}
	return false
}
