package chain

import (
	"testing"

	"github.com/macromarkets/vault/pkg/types"
	"github.com/macromarkets/vault/tests/mocks"
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	mock := mocks.NewMockChain("eth")
	r.RegisterChain(mock)

	got, err := r.Chain("eth")
	if err != nil {
		t.Fatalf("expected chain, got error: %v", err)
	}
	if got.ID() != "eth" {
		t.Errorf("expected id=eth, got %s", got.ID())
	}
}

func TestRegistry_ChainNotFound(t *testing.T) {
	r := NewRegistry()
	_, err := r.Chain("nonexistent")
	if err == nil {
		t.Fatal("expected error for unregistered chain")
	}
}

func TestRegistry_ChainIDs(t *testing.T) {
	r := NewRegistry()
	r.RegisterChain(mocks.NewMockChain("eth"))
	r.RegisterChain(mocks.NewMockChain("btc"))
	r.RegisterChain(mocks.NewMockChain("sol"))

	ids := r.ChainIDs()
	if len(ids) != 3 {
		t.Errorf("expected 3 chains, got %d", len(ids))
	}
}

func TestRegistry_OverwriteChain(t *testing.T) {
	r := NewRegistry()
	r.RegisterChain(mocks.NewMockChain("eth"))
	
	mock2 := mocks.NewMockChain("eth")
	mock2.NameVal = "Ethereum v2"
	r.RegisterChain(mock2)

	got, _ := r.Chain("eth")
	if got.Name() != "Ethereum v2" {
		t.Errorf("expected overwritten chain, got %s", got.Name())
	}
}

func TestRegistry_RegisterToken(t *testing.T) {
	r := NewRegistry()
	r.RegisterToken(types.Token{Symbol: "usdt", ChainID: "eth", Decimals: 6, Contract: "0xabc"})
	r.RegisterToken(types.Token{Symbol: "usdc", ChainID: "eth", Decimals: 6, Contract: "0xdef"})
	r.RegisterToken(types.Token{Symbol: "usdt", ChainID: "polygon", Decimals: 6, Contract: "0x123"})

	ethTokens := r.TokensForChain("eth")
	if len(ethTokens) != 2 {
		t.Errorf("expected 2 ETH tokens, got %d", len(ethTokens))
	}

	polyTokens := r.TokensForChain("polygon")
	if len(polyTokens) != 1 {
		t.Errorf("expected 1 polygon token, got %d", len(polyTokens))
	}
}

func TestRegistry_FindToken(t *testing.T) {
	r := NewRegistry()
	r.RegisterToken(types.Token{Symbol: "usdt", ChainID: "eth", Decimals: 6})
	r.RegisterToken(types.Token{Symbol: "usdc", ChainID: "eth", Decimals: 6})

	tests := []struct {
		chain, symbol string
		wantErr       bool
	}{
		{"eth", "usdt", false},
		{"eth", "usdc", false},
		{"eth", "dai", true},
		{"btc", "usdt", true},
		{"", "usdt", true},
	}
	for _, tt := range tests {
		t.Run(tt.chain+"/"+tt.symbol, func(t *testing.T) {
			tok, err := r.FindToken(tt.chain, tt.symbol)
			if tt.wantErr && err == nil {
				t.Error("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tt.wantErr && tok.Symbol != tt.symbol {
				t.Errorf("expected %s, got %s", tt.symbol, tok.Symbol)
			}
		})
	}
}

func TestRegistry_TokensForChain_Empty(t *testing.T) {
	r := NewRegistry()
	if len(r.TokensForChain("btc")) != 0 {
		t.Error("expected empty token list for btc")
	}
}

func TestAllTokens_Complete(t *testing.T) {
	tokens := AllTokens()
	if len(tokens) < 6 {
		t.Fatalf("expected at least 6 tokens, got %d", len(tokens))
	}
	for _, tok := range tokens {
		if tok.Symbol == "" || tok.Contract == "" || tok.ChainID == "" || tok.Decimals == 0 {
			t.Errorf("token %s/%s has missing fields", tok.ChainID, tok.Symbol)
		}
	}
	// Verify specific chains have tokens
	chainCounts := map[string]int{}
	for _, tok := range tokens {
		chainCounts[tok.ChainID]++
	}
	for _, chain := range []string{"eth", "polygon", "sol"} {
		if chainCounts[chain] < 2 {
			t.Errorf("chain %s should have at least 2 tokens, got %d", chain, chainCounts[chain])
		}
	}
}
