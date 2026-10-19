package chain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func TestEVM_ChainID_ReadsTheHexAnswer(t *testing.T) {
	sepolia := &answeringCaller{results: map[string]string{"eth_chainId": `"0xaa36a7"`}}
	id, err := EVMChainID(context.Background(), sepolia)
	if err != nil || id != 11155111 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	unreachable := &answeringCaller{err: ErrProviderUnavailable}
	if _, err := EVMChainID(context.Background(), unreachable); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v", err)
	}
	garbled := &answeringCaller{results: map[string]string{"eth_chainId": `"0xzz"`}}
	if _, err := EVMChainID(context.Background(), garbled); err == nil || !strings.Contains(err.Error(), "eth_chainId returned") {
		t.Fatalf("err = %v", err)
	}
}

func TestSolana_Genesis_RefusesAnEmptyAnswer(t *testing.T) {
	const devnet = "EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG"
	same := &answeringCaller{results: map[string]string{"getGenesisHash": `"` + devnet + `"`}}
	if got, err := SolanaGenesis(context.Background(), same); err != nil || got != devnet {
		t.Fatalf("got=%q err=%v", got, err)
	}
	empty := &answeringCaller{results: map[string]string{"getGenesisHash": `""`}}
	if _, err := SolanaGenesis(context.Background(), empty); err == nil {
		t.Fatal("an empty genesis is an unusable answer")
	}
}

func TestRPC_EndpointFingerprint_IsKeyedWithTheAppKey(t *testing.T) {
	const url = "http://5.104.72.66:8545"
	t.Cleanup(func() { SetRPCFingerprintKey(nil) })

	SetRPCFingerprintKey([]byte("0123456789abcdef0123456789abcdef"))
	keyed := RPCEndpointFingerprint(url)
	sum := sha256.Sum256([]byte(url))
	if keyed == hex.EncodeToString(sum[:])[:rpcFingerprintLength] {
		t.Fatal("an unsalted digest of a keyless URL can be guessed by hashing candidate hosts")
	}
	if keyed != RPCEndpointFingerprint(url) || len(keyed) != rpcFingerprintLength {
		t.Fatalf("fingerprint = %q", keyed)
	}
	SetRPCFingerprintKey([]byte("another-key-another-key-another!"))
	if RPCEndpointFingerprint(url) == keyed {
		t.Fatal("another key must give another fingerprint")
	}
}
