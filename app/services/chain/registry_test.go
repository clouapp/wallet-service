package chain

import (
	"errors"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

func TestRegistry_Register_AndGet(t *testing.T) {
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

func TestRegistry_Chain_NotFound(t *testing.T) {
	r := NewRegistry()
	_, err := r.Chain("nonexistent")
	if !errors.Is(err, ErrUnknownChain) {
		t.Fatalf("err = %v", err)
	}
}

func TestRegistry_Chain_IDs(t *testing.T) {
	r := NewRegistry()
	r.RegisterChain(mocks.NewMockChain("eth"))
	r.RegisterChain(mocks.NewMockChain("btc"))
	r.RegisterChain(mocks.NewMockChain("sol"))

	ids := r.ChainIDs()
	if len(ids) != 3 {
		t.Errorf("expected 3 chains, got %d", len(ids))
	}
}

func TestRegistry_Overwrite_Chain(t *testing.T) {
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

func TestRegistry_Register_Token(t *testing.T) {
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

func TestRegistry_Find_Token(t *testing.T) {
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

func TestFind_TokenByContract_CaseInsensitiveEVM(t *testing.T) {
	r := NewRegistry()
	r.RegisterToken(types.Token{
		Symbol: models.SymbolUSDT, ChainID: models.ChainETH, Decimals: 6,
		Contract: models.USDTContractETH,
	})
	got, err := r.FindTokenByContract(models.ChainETH, strings.ToLower(models.USDTContractETH))
	if err != nil {
		t.Fatal(err)
	}
	if got.Symbol != models.SymbolUSDT || got.Decimals != 6 {
		t.Fatalf("got %+v", got)
	}
}

func TestFind_TokenByContract_SolanaMintExact(t *testing.T) {
	r := NewRegistry()
	r.RegisterToken(types.Token{
		Symbol: models.SymbolUSDC, ChainID: models.ChainSOL, Decimals: 6,
		Contract: models.USDCMintSOL,
	})
	if _, err := r.FindTokenByContract(models.ChainSOL, strings.ToLower(models.USDCMintSOL)); err == nil {
		t.Fatal("sol mint match must stay case-sensitive")
	}
	got, err := r.FindTokenByContract(models.ChainSOL, models.USDCMintSOL)
	if err != nil || got.Decimals != 6 {
		t.Fatalf("got %+v err %v", got, err)
	}
}

// listChain is a chain adapter that takes a whole endpoint list.
type listChain struct {
	types.Chain
	primary   string
	fallbacks []RPCFallback
}

func (c *listChain) ReplaceEndpoints(primary string, fallbacks []RPCFallback) {
	c.primary, c.fallbacks = primary, fallbacks
}

func TestRegistry_ReplaceEndpoints_RetargetsAChainThatTakesAList(t *testing.T) {
	r := NewRegistry()
	adapter := &listChain{Chain: mocks.NewMockChain(models.ChainETH)}
	r.RegisterChain(adapter)
	r.RegisterChain(mocks.NewMockChain(models.ChainBTC))

	replaced, err := r.ReplaceEndpoints(models.ChainETH, " https://primary.example ", []RPCFallback{{URL: "https://fallback.example", Position: 2}})
	if err != nil || !replaced || adapter.primary != "https://primary.example" || len(adapter.fallbacks) != 1 {
		t.Fatalf("replaced=%v err=%v adapter=%+v", replaced, err, adapter)
	}
	if replaced, err := r.ReplaceEndpoints("missing", "https://primary.example", nil); err != nil || replaced {
		t.Fatalf("a chain that is not loaded: replaced=%v err=%v", replaced, err)
	}
	if _, err := r.ReplaceEndpoints(models.ChainBTC, "https://primary.example", nil); err == nil {
		t.Fatal("a chain that takes a single endpoint must be refused")
	}
	if _, err := r.ReplaceEndpoints(models.ChainETH, " ", nil); err == nil || adapter.primary != "https://primary.example" {
		t.Fatal("an empty primary must not wipe the list")
	}
}
