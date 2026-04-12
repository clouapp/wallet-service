package refresh

import (
	"testing"
)

func TestExpandScopesForEVMFull(t *testing.T) {
	req := RefreshRequest{ChainID: "eth", Scope: RefreshScopeFull}
	scopes, err := ExpandScopes(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []RefreshScope{RefreshScopeBalances, RefreshScopeTransactions, RefreshScopeTokens}
	assertScopes(t, expected, scopes)
}

func TestExpandScopesForBitcoinFull(t *testing.T) {
	req := RefreshRequest{ChainID: "btc", Scope: RefreshScopeFull}
	scopes, err := ExpandScopes(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []RefreshScope{RefreshScopeBalances, RefreshScopeTransactions, RefreshScopeUtxos}
	assertScopes(t, expected, scopes)
}

func TestExpandScopesForSolanaFull(t *testing.T) {
	req := RefreshRequest{ChainID: "sol", Scope: RefreshScopeFull}
	scopes, err := ExpandScopes(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []RefreshScope{RefreshScopeBalances, RefreshScopeTransactions, RefreshScopeTokens}
	assertScopes(t, expected, scopes)
}

func TestExpandScopesNonFull(t *testing.T) {
	req := RefreshRequest{ChainID: "eth", Scope: RefreshScopeBalances}
	scopes, err := ExpandScopes(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scopes) != 1 {
		t.Fatalf("expected 1 scope, got %d", len(scopes))
	}
	if scopes[0] != RefreshScopeBalances {
		t.Errorf("expected %q, got %q", RefreshScopeBalances, scopes[0])
	}
}

func TestExpandScopesUnknownChain(t *testing.T) {
	req := RefreshRequest{ChainID: "unknown", Scope: RefreshScopeFull}
	_, err := ExpandScopes(req)
	if err == nil {
		t.Fatal("expected error for unknown chain, got nil")
	}
}

func TestExpandScopesForTestnetEVM(t *testing.T) {
	for _, chainID := range []string{"teth", "tpolygon"} {
		req := RefreshRequest{ChainID: chainID, Scope: RefreshScopeFull}
		scopes, err := ExpandScopes(req)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", chainID, err)
		}
		if len(scopes) != 3 {
			t.Fatalf("%s: expected 3 scopes for full, got %d", chainID, len(scopes))
		}
	}
}

func TestExpandScopesForTestnetBitcoin(t *testing.T) {
	req := RefreshRequest{ChainID: "tbtc", Scope: RefreshScopeFull}
	scopes, err := ExpandScopes(req)
	if err != nil {
		t.Fatal(err)
	}
	hasUtxos := false
	for _, s := range scopes {
		if s == RefreshScopeUtxos {
			hasUtxos = true
		}
	}
	if !hasUtxos {
		t.Fatal("expected utxos scope for tbtc full")
	}
}

func TestExpandScopesForTestnetSolana(t *testing.T) {
	req := RefreshRequest{ChainID: "tsol", Scope: RefreshScopeFull}
	scopes, err := ExpandScopes(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 3 {
		t.Fatalf("expected 3, got %d", len(scopes))
	}
}

func TestExpandScopesSingleScopePassesThrough(t *testing.T) {
	for _, scope := range []RefreshScope{RefreshScopeBalances, RefreshScopeTransactions, RefreshScopeTokens, RefreshScopeUtxos} {
		scopes, err := ExpandScopes(RefreshRequest{ChainID: "eth", Scope: scope})
		if err != nil {
			t.Fatalf("scope %s: %v", scope, err)
		}
		if len(scopes) != 1 || scopes[0] != scope {
			t.Fatalf("scope %s: expected [%s], got %v", scope, scope, scopes)
		}
	}
}

func TestExpandScopesAllMainnetChains(t *testing.T) {
	cases := []struct {
		chainID   string
		wantCount int
		wantUtxos bool
	}{
		{"eth", 3, false},
		{"polygon", 3, false},
		{"sol", 3, false},
		{"btc", 3, true},
	}
	for _, tc := range cases {
		req := RefreshRequest{ChainID: tc.chainID, Scope: RefreshScopeFull}
		scopes, err := ExpandScopes(req)
		if err != nil {
			t.Fatalf("%s: %v", tc.chainID, err)
		}
		if len(scopes) != tc.wantCount {
			t.Fatalf("%s: expected %d scopes, got %d", tc.chainID, tc.wantCount, len(scopes))
		}
		hasUtxos := false
		for _, s := range scopes {
			if s == RefreshScopeUtxos {
				hasUtxos = true
			}
		}
		if hasUtxos != tc.wantUtxos {
			t.Fatalf("%s: utxos=%v, want %v", tc.chainID, hasUtxos, tc.wantUtxos)
		}
	}
}

func assertScopes(t *testing.T, expected, actual []RefreshScope) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("expected %d scopes, got %d: %v", len(expected), len(actual), actual)
	}
	for i, s := range expected {
		if actual[i] != s {
			t.Errorf("scope[%d]: expected %q, got %q", i, s, actual[i])
		}
	}
}
