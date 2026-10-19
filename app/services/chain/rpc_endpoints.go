package chain

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// ErrWrongNetwork is an RPC endpoint that answers for another network than the
// chain's: another EVM chain id or another Solana genesis.
var ErrWrongNetwork = errors.New("rpc endpoint serves another network")

const (
	evmChainIDMethod     = "eth_chainId"
	solanaGenesisMethod  = "getGenesisHash"
	rpcFingerprintLength = 12
)

// RPCFallback is one fallback RPC endpoint handed to an adapter: the URL to call
// and its position in the chain's stored list (2 and after). Logs and health name
// it by that position, so a disabled endpoint before it does not shift the number
// an operator sees. A zero Position is the endpoint's place in the list it came in.
type RPCFallback struct {
	URL      string
	Position int
}

// RPCEndpointHealth is what one process last saw of one RPC endpoint of a chain.
// Host is the URL's host (and port) only and Fingerprint identifies the URL
// without revealing it, so neither carries a path, a query or a key.
type RPCEndpointHealth struct {
	Position            int
	Host                string
	Fingerprint         string
	ConsecutiveFailures int
	CoolingDownUntil    time.Time
	LastError           string
	LastErrorAt         time.Time
	WrongNetwork        bool
}

// RPCHealthSnapshot is the endpoint health of one chain as the last process that
// saw it change recorded it. Process is the LAMBDA_MODE of that process ("local"
// for the HTTP server, its workers and artisan).
type RPCHealthSnapshot struct {
	ObservedAt time.Time
	Process    string
	Endpoints  []RPCEndpointHealth
}

// RPCHealthSink receives the endpoint health of a chain each time it changes
// (an endpoint starts failing, is skipped for a cool-down, or recovers). It must
// not block the call that reports it.
type RPCHealthSink interface {
	RecordRPCHealth(chainID string, endpoints []RPCEndpointHealth)
}

var fingerprintKey atomic.Pointer[[]byte]

// SetRPCFingerprintKey keys RPCEndpointFingerprint (APP_KEY, set at boot by the chain
// provider, the same in every process so their health records match). Nil unkeys it.
func SetRPCFingerprintKey(key []byte) {
	if key == nil {
		fingerprintKey.Store(nil)
		return
	}
	copied := append([]byte(nil), key...)
	fingerprintKey.Store(&copied)
}

// RPCEndpointFingerprint is a short HMAC-SHA256 of rawURL under the fingerprint key:
// equal URLs have equal fingerprints, and neither the URL (with its key) nor, for a
// keyless URL, its host can be found again by hashing candidates.
func RPCEndpointFingerprint(rawURL string) string {
	var key []byte
	if stored := fingerprintKey.Load(); stored != nil {
		key = *stored
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(strings.TrimSpace(rawURL)))
	return hex.EncodeToString(mac.Sum(nil))[:rpcFingerprintLength]
}

// RPCEndpointHost is the host[:port] of rawURL, without user info, path or query,
// or "" when rawURL does not parse. It is the only part of an endpoint that is
// printed or logged.
func RPCEndpointHost(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Host)
}

// EVMChainID asks caller for eth_chainId and returns it as a number.
func EVMChainID(ctx context.Context, caller JSONRPCCaller) (int64, error) {
	if caller == nil {
		return 0, fmt.Errorf("%s: json-rpc client is not linked", evmChainIDMethod)
	}
	var chainIDHex string
	if err := caller.Call(ctx, evmChainIDMethod, &chainIDHex); err != nil {
		return 0, fmt.Errorf("%s: %w", evmChainIDMethod, err)
	}
	digits := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(chainIDHex)), "0x")
	networkID, err := strconv.ParseInt(digits, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("%s returned %q: %w", evmChainIDMethod, chainIDHex, err)
	}
	return networkID, nil
}

// SolanaGenesis asks caller for getGenesisHash.
func SolanaGenesis(ctx context.Context, caller JSONRPCCaller) (string, error) {
	if caller == nil {
		return "", fmt.Errorf("%s: json-rpc client is not linked", solanaGenesisMethod)
	}
	var genesis string
	if err := caller.Call(ctx, solanaGenesisMethod, &genesis); err != nil {
		return "", fmt.Errorf("%s: %w", solanaGenesisMethod, err)
	}
	genesis = strings.TrimSpace(genesis)
	if genesis == "" {
		return "", fmt.Errorf("%s returned an empty hash", solanaGenesisMethod)
	}
	return genesis, nil
}
