package chainregistry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
)

// endpointVerifyTimeout bounds each question asked of an endpoint before it is saved.
const endpointVerifyTimeout = 15 * time.Second

// ErrEndpointIdentityUnsupported is a chain whose endpoints cannot be asked which
// network they serve (Bitcoin family, TRON, XRP): nothing is verified.
var ErrEndpointIdentityUnsupported = errors.New("this chain's rpc endpoints cannot be asked which network they serve")

// ErrEndpointUnverified is an endpoint that did not answer which network it serves
// (unreachable, timed out, refused the call). It is not saved.
var ErrEndpointUnverified = errors.New("rpc endpoint did not say which network it serves")

// noNetworkIDHelp says how to get past an EVM chain without network_id.
const noNetworkIDHelp = "the chain has no network_id, so eth_chainId cannot be compared: set it " +
	"(chains:align-network --apply sets it on a primary record, from CHAIN_NETWORK_PROFILE) or, " +
	"from artisan only, pass --skip-network-check"

// EndpointTarget is the network an endpoint of a chain must serve: the EVM chain id
// it signs with, or the Solana cluster.
type EndpointTarget struct {
	Network      string
	EVMNetworkID int64
}

// ChainEndpointTarget is the network record is on now: network_id on EVM, the
// cluster its primary endpoint resolves to on Solana (is_testnet when the host does
// not say). primaryURL is the opened, resolved primary; it may be empty.
func ChainEndpointTarget(record models.Chain, primaryURL string) EndpointTarget {
	target := EndpointTarget{Network: record.ResolveNetwork(primaryURL).Name}
	if record.AdapterType == models.AdapterTypeEVM && record.NetworkID != nil {
		target.EVMNetworkID = *record.NetworkID
	}
	return target
}

// ProfileEndpointTarget is the network the profile names for a primary record, or
// false for a record the profile does not decide (the t-prefixed ones).
func ProfileEndpointTarget(profile string, record models.Chain) (EndpointTarget, bool) {
	spec, ok, err := models.PrimaryChainNetwork(strings.TrimSpace(profile), record.ID)
	if err != nil || !ok {
		return EndpointTarget{}, false
	}
	target := EndpointTarget{Network: spec.Network}
	if spec.NetworkID != nil {
		target.EVMNetworkID = *spec.NetworkID
	}
	return target, true
}

// VerifyEndpoint asks rawURL which network it serves and returns that network's
// name when it is target: an EVM endpoint answers eth_chainId, which must be the
// target chain id; a Solana endpoint answers getGenesisHash, which must be the
// target cluster's. Another network is chain.ErrWrongNetwork, an endpoint that does
// not answer is ErrEndpointUnverified, and any other adapter is
// ErrEndpointIdentityUnsupported. No URL is in an error.
func VerifyEndpoint(ctx context.Context, adapterType string, target EndpointTarget, rawURL string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, endpointVerifyTimeout)
	defer cancel()
	caller := chain.NewJSONRPCCaller(chain.JSONRPCDeps{URL: rawURL})
	switch adapterType {
	case models.AdapterTypeEVM:
		if target.EVMNetworkID <= 0 {
			return "", fmt.Errorf("%w: %s", ErrEndpointUnverified, noNetworkIDHelp)
		}
		served, err := chain.EVMChainID(ctx, caller)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrEndpointUnverified, err)
		}
		if served != target.EVMNetworkID {
			return evmNetworkLabel(served), fmt.Errorf("%w: eth_chainId is %d (%s), the chain signs for %d (%s)",
				chain.ErrWrongNetwork, served, evmNetworkLabel(served), target.EVMNetworkID, evmNetworkLabel(target.EVMNetworkID))
		}
		return evmNetworkLabel(served), nil
	case models.AdapterTypeSolana:
		want := models.SolanaGenesisHash(target.Network)
		if want == "" {
			return "", fmt.Errorf("%w: the chain's cluster %q has no known genesis hash", ErrEndpointUnverified, target.Network)
		}
		genesis, err := chain.SolanaGenesis(ctx, caller)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrEndpointUnverified, err)
		}
		served := models.SolanaNetworkOfGenesis(genesis)
		if genesis != want {
			if served == "" {
				served = "genesis " + genesis
			}
			return served, fmt.Errorf("%w: it serves %s, the chain is on %s", chain.ErrWrongNetwork, served, target.Network)
		}
		return served, nil
	default:
		return "", ErrEndpointIdentityUnsupported
	}
}

// EndpointProbe is what an endpoint answered when asked now: the network it serves
// and its head (latest block or slot), with the time both took.
type EndpointProbe struct {
	Network string
	Head    uint64
	Latency time.Duration
	Err     error
}

// ProbeEndpoint verifies rawURL against target (VerifyEndpoint) and reads its head:
// eth_blockNumber on EVM, getSlot (finalized) on Solana. Err is the first failure.
func ProbeEndpoint(ctx context.Context, adapterType string, target EndpointTarget, rawURL string) EndpointProbe {
	started := time.Now()
	network, err := VerifyEndpoint(ctx, adapterType, target, rawURL)
	probe := EndpointProbe{Network: network, Err: err}
	if err == nil {
		probe.Head, probe.Err = endpointHead(ctx, adapterType, rawURL)
	}
	probe.Latency = time.Since(started)
	return probe
}

func endpointHead(ctx context.Context, adapterType, rawURL string) (uint64, error) {
	ctx, cancel := context.WithTimeout(ctx, endpointVerifyTimeout)
	defer cancel()
	caller := chain.NewJSONRPCCaller(chain.JSONRPCDeps{URL: rawURL})
	if caller == nil {
		return 0, fmt.Errorf("json-rpc client is not linked")
	}
	switch adapterType {
	case models.AdapterTypeEVM:
		var head string
		if err := caller.Call(ctx, "eth_blockNumber", &head); err != nil {
			return 0, fmt.Errorf("eth_blockNumber: %w", err)
		}
		var block uint64
		if _, err := fmt.Sscanf(strings.TrimPrefix(strings.ToLower(head), "0x"), "%x", &block); err != nil {
			return 0, fmt.Errorf("eth_blockNumber returned %q", head)
		}
		return block, nil
	case models.AdapterTypeSolana:
		var slot uint64
		if err := caller.Call(ctx, "getSlot", &slot, map[string]string{"commitment": "finalized"}); err != nil {
			return 0, fmt.Errorf("getSlot: %w", err)
		}
		return slot, nil
	default:
		return 0, ErrEndpointIdentityUnsupported
	}
}

func evmNetworkLabel(networkID int64) string {
	if name := models.EVMNetworkName(networkID); name != "" {
		return name
	}
	return fmt.Sprintf("evm chain %d", networkID)
}
