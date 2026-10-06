package bitcoin

import (
	"math/big"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/pkg/types"
)

func TestBitcoin_ValidateAddress(t *testing.T) {
	adapter := NewBitcoinLive(BitcoinConfig{ChainIDStr: "btc", ChainName: "Bitcoin", NativeSymbol: "btc", RPCURL: "http://fake", Confirmations: 3})

	tests := []struct {
		addr string
		want bool
	}{
		{"bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4", true},    // bech32
		{"1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", true},            // P2PKH
		{"3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy", true},            // P2SH
		{"bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq", true},    // bech32
		{"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12", false},   // EVM
		{"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", false}, // Solana
		{"", false},
		{"short", false},
		{"2A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", false}, // invalid prefix
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := adapter.ValidateAddress(tt.addr); got != tt.want {
				t.Errorf("ValidateAddress(%s) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestBitcoin_Identity(t *testing.T) {
	a := NewBitcoinLive(BitcoinConfig{ChainIDStr: "btc", ChainName: "Bitcoin", NativeSymbol: "btc", RPCURL: "http://fake", Confirmations: 3})
	if a.ID() != "btc" {
		t.Errorf("expected btc, got %s", a.ID())
	}
	if a.Name() != "Bitcoin" {
		t.Errorf("expected Bitcoin, got %s", a.Name())
	}
	if a.NativeAsset() != "btc" {
		t.Errorf("expected btc, got %s", a.NativeAsset())
	}
	if a.RequiredConfirmations() != 3 {
		t.Errorf("expected 3, got %d", a.RequiredConfirmations())
	}
}

func TestBitcoinAmountsUseTheChainRowDecimals(t *testing.T) {
	eight := NewBitcoinLive(BitcoinConfig{NativeDecimal: 8})
	sats, err := eight.btcToSats(decimal.RequireFromString("1.5"))
	if err != nil || sats.Cmp(big.NewInt(150_000_000)) != 0 || eight.NativeDecimals() != 8 {
		t.Fatalf("8-decimal row: %s err %v decimals %d", sats, err, eight.NativeDecimals())
	}
	two := NewBitcoinLive(BitcoinConfig{NativeDecimal: 2, NativeSymbol: "COIN"})
	units, err := two.btcToSats(decimal.RequireFromString("1.5"))
	if err != nil || units.Cmp(big.NewInt(150)) != 0 || fmtUnits(units, two.cfg.NativeDecimal) != "1.5" {
		t.Fatalf("2-decimal row: %s formatted %s err %v", units, fmtUnits(units, two.cfg.NativeDecimal), err)
	}
}

func TestBitcoin_GetTokenBalance_Error(t *testing.T) {
	a := NewBitcoinLive(BitcoinConfig{ChainIDStr: "btc", ChainName: "Bitcoin", NativeSymbol: "btc", RPCURL: "http://fake", Confirmations: 3})
	_, err := a.GetTokenBalance(nil, "someaddr", types.Token{Symbol: "usdt"})
	if err == nil {
		t.Error("expected error — bitcoin doesn't support tokens")
	}
}
