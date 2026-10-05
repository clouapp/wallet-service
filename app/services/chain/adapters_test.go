package chain

import (
	"testing"

	"github.com/macrowallets/waas/pkg/types"
)

func TestSolana_ValidateAddress(t *testing.T) {
	adapter := NewSolanaLive(SolanaConfig{ChainIDStr: "sol", ChainName: "Solana", NativeSymbol: "sol", RPCURL: "http://fake", Confirmations: 1})

	tests := []struct {
		addr string
		want bool
	}{
		{"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", true},
		{"So11111111111111111111111111111111111111112", true},
		{"4fYNw3dojWmQ4dXtSGE9epjRGy9pFSx62YypT7avPYvA", true},
		{"short", false},
		{"", false},
		{"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12", false}, // EVM address
		// Base58 excludes 0, O, I, l
		{"0EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1", false}, // has 0
		{"OEPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1", false}, // has O
		{"IEPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1", false}, // has I
		{"lEPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1", false}, // has l
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := adapter.ValidateAddress(tt.addr); got != tt.want {
				t.Errorf("ValidateAddress(%s) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestSolana_Identity(t *testing.T) {
	a := NewSolanaLive(SolanaConfig{ChainIDStr: "sol", ChainName: "Solana", NativeSymbol: "sol", RPCURL: "http://fake", Confirmations: 1})
	if a.ID() != "sol" {
		t.Errorf("expected sol, got %s", a.ID())
	}
	if a.Name() != "Solana" {
		t.Errorf("expected Solana, got %s", a.Name())
	}
	if a.NativeAsset() != "sol" {
		t.Errorf("expected sol, got %s", a.NativeAsset())
	}
	if a.RequiredConfirmations() != 1 {
		t.Errorf("expected 1, got %d", a.RequiredConfirmations())
	}
}

func TestBitcoin_ValidateAddress(t *testing.T) {
	adapter := NewBitcoinLive(BitcoinConfig{ChainIDStr: "btc", ChainName: "Bitcoin", NativeSymbol: "btc", RPCURL: "http://fake", Confirmations: 3})

	// Only P2WPKH destinations of the adapter's network are valid: the builder pays
	// nothing else (BIP-173 / BIP-350 vectors for the SegWit cases).
	tests := []struct {
		addr string
		want bool
	}{
		{"bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4", true},                      // P2WPKH
		{"BC1QW508D6QEJXTDG4Y5R3ZARVARY0C5XW7KV8F3T4", true},                      // P2WPKH, uppercase
		{"bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq", true},                      // P2WPKH
		{"bc1qrp33g0q5c5txsp9arysrx4k6zdkfs4nce4xj0gdcccefvpysxf3qccfmv3", false}, // P2WSH
		{"bc1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7vqzk5jj0", false}, // taproot
		{"bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t5", false},                     // bad checksum
		{"tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx", false},                     // testnet P2WPKH
		{"ltc1qhdhvrwe6rgqns8fz28tee0hphr5x7ulw5exv4w", false},                    // Litecoin P2WPKH
		{"1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", false},                             // P2PKH
		{"3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy", false},                             // P2SH
		{"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12", false},                     // EVM
		{"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", false},                   // Solana
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

func TestBitcoin_GetTokenBalance_Error(t *testing.T) {
	a := NewBitcoinLive(BitcoinConfig{ChainIDStr: "btc", ChainName: "Bitcoin", NativeSymbol: "btc", RPCURL: "http://fake", Confirmations: 3})
	_, err := a.GetTokenBalance(nil, "someaddr", types.Token{Symbol: "usdt"})
	if err == nil {
		t.Error("expected error — bitcoin doesn't support tokens")
	}
}
