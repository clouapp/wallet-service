package chain

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// answeringCaller answers each method with a fixed JSON result, or fails.
type answeringCaller struct {
	results map[string]string
	err     error
	asked   []string
}

func (c *answeringCaller) Call(_ context.Context, method string, out interface{}, _ ...interface{}) error {
	c.asked = append(c.asked, method)
	if c.err != nil {
		return c.err
	}
	return json.Unmarshal([]byte(c.results[method]), out)
}

func TestRPC_EndpointFingerprint_IsStableAndHidesTheURL(t *testing.T) {
	const url = "https://eth-sepolia.g.alchemy.com/v2/secret-key"
	first := RPCEndpointFingerprint(url)
	if first != RPCEndpointFingerprint(" "+url+" ") || len(first) != rpcFingerprintLength {
		t.Fatalf("fingerprint = %q", first)
	}
	if first == RPCEndpointFingerprint("https://eth-sepolia.g.alchemy.com/v2/other-key") || strings.Contains(first, "secret") {
		t.Fatalf("fingerprint does not tell two keys apart or leaks the URL: %q", first)
	}
}

func TestRPC_EndpointHost_KeepsOnlyTheHost(t *testing.T) {
	cases := map[string]string{
		"https://user:pass@Sepolia.DRPC.org/v2/key?api-key=x": "sepolia.drpc.org",
		"http://5.104.72.66:8545":                             "5.104.72.66:8545",
		"::not a url":                                         "",
	}
	for raw, want := range cases {
		if got := RPCEndpointHost(raw); got != want {
			t.Errorf("%q: host = %q, want %q", raw, got, want)
		}
	}
}

func TestCheck_EVMNetwork_RefusesAnotherChainID(t *testing.T) {
	sepolia := &answeringCaller{results: map[string]string{"eth_chainId": `"0xaa36a7"`}}
	if err := CheckEVMNetwork(context.Background(), sepolia, 11155111); err != nil {
		t.Fatalf("Sepolia endpoint refused: %v", err)
	}
	mainnet := &answeringCaller{results: map[string]string{"eth_chainId": `"0x1"`}}
	err := CheckEVMNetwork(context.Background(), mainnet, 11155111)
	if !errors.Is(err, ErrWrongNetwork) || !strings.Contains(err.Error(), "eth_chainId is 1") {
		t.Fatalf("err = %v", err)
	}
	unreachable := &answeringCaller{err: ErrProviderUnavailable}
	if err := CheckEVMNetwork(context.Background(), unreachable, 11155111); errors.Is(err, ErrWrongNetwork) || !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("an unreachable endpoint is not a wrong network: %v", err)
	}
	if err := CheckEVMNetwork(context.Background(), unreachable, 0); err != nil || len(unreachable.asked) != 1 {
		t.Fatalf("a chain without network id must not be checked: err=%v asked=%v", err, unreachable.asked)
	}
}

func TestCheck_SolanaGenesis_RefusesAnotherCluster(t *testing.T) {
	const devnet = "EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG"
	mainnet := &answeringCaller{results: map[string]string{"getGenesisHash": `"5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d"`}}
	if err := CheckSolanaGenesis(context.Background(), mainnet, devnet); !errors.Is(err, ErrWrongNetwork) {
		t.Fatalf("err = %v", err)
	}
	same := &answeringCaller{results: map[string]string{"getGenesisHash": `"` + devnet + `"`}}
	if err := CheckSolanaGenesis(context.Background(), same, devnet); err != nil {
		t.Fatalf("devnet endpoint refused: %v", err)
	}
	empty := &answeringCaller{results: map[string]string{"getGenesisHash": `""`}}
	if err := CheckSolanaGenesis(context.Background(), empty, devnet); err == nil || errors.Is(err, ErrWrongNetwork) {
		t.Fatalf("an empty genesis is an unusable answer, not a network: %v", err)
	}
}
