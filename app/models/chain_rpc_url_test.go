package models

import (
	"strings"
	"testing"
)

func TestChainRpcUrl_Resolve_RPCURL(t *testing.T) {
	env := map[string]string{"SOLANA_RPC_URL": "https://solana-devnet.example/key", "BLANK": "  "}
	lookup := func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	}
	cases := []struct {
		stored, want string
		wantErr      bool
	}{
		{"https://api.devnet.solana.com", "https://api.devnet.solana.com", false},
		{"env:SOLANA_RPC_URL", "https://solana-devnet.example/key", false},
		{"env:MISSING_RPC_URL", "", true},
		{"env:BLANK", "", true},
		{"env:lower", "", true},
		{"env:", "", true},
	}
	for _, tc := range cases {
		got, err := resolveRPCURL(tc.stored, lookup)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Fatalf("%q: got %q, %v", tc.stored, got, err)
		}
		if err != nil && strings.Contains(err.Error(), "example/key") {
			t.Fatalf("error must not leak the url: %v", err)
		}
	}
}

func TestDial_Endpoint_UsesAStoredURLInsteadOfTheEnvironment(t *testing.T) {
	t.Setenv("ETH_RPC_URL", "https://env-fallback.invalid/secret-env")
	const stored = "https://dial.example/v2/route-key"
	got, err := DialEndpoint(stored)
	if err != nil {
		t.Fatal("dial endpoint failed")
	}
	if got != stored || strings.Contains(got, "env-fallback") {
		t.Fatal("dial endpoint did not keep the stored url")
	}
	_, err = DialEndpoint("not a url")
	if err == nil || strings.Contains(err.Error(), "not a url") {
		t.Fatal("unusable endpoint error leaked the value")
	}
}

func TestRPCURL_Env_ReferenceClassifiesBitcoinTestnet4OnceResolved(t *testing.T) {
	lookup := func(name string) (string, bool) {
		if name == "BTC_RPC_URL" {
			return " https://mempool.space/testnet4/api ", true
		}
		return "", false
	}
	chain := &Chain{AdapterType: AdapterTypeBitcoin, IsTestnet: true}

	if got := chain.ResolveNetwork("env:BTC_RPC_URL"); got.Name != NetworkBitcoinTestnet {
		t.Fatalf("an unresolved reference names no URL; resolved %+v", got)
	}
	resolved, err := resolveRPCURL("env:BTC_RPC_URL", lookup)
	if err != nil {
		t.Fatal(err)
	}
	if got := chain.ResolveNetwork(resolved); got.Name != NetworkBitcoinTestnet4 || !got.Testnet {
		t.Fatalf("resolved %+v", got)
	}
}

func TestRPCURL_Env_ReferenceStillClassifiesSolanaDevnet(t *testing.T) {
	chain := &Chain{AdapterType: AdapterTypeSolana, IsTestnet: true}
	got := chain.ResolveNetwork("https://solana-devnet.g.alchemy.com/v2/redacted")
	if got.Name != NetworkSolanaDevnet || !got.Testnet {
		t.Fatalf("resolved %+v", got)
	}
}
