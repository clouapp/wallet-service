package solana

import (
	"context"
	"math/big"
	"testing"

	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/types"
)

func TestSolana_Validate_Address(t *testing.T) {
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

func TestIdentity_Solana_Identity(t *testing.T) {
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

func TestSolana_Fee_UsesChainRowDecimals(t *testing.T) {
	nine := NewSolanaLive(SolanaConfig{NativeSymbol: "SOL", NativeDecimal: 9})
	estimate, err := nine.EstimateFee(context.Background(), types.TransferRequest{})
	if err != nil || estimate.Fee != amount.FormatBaseUnits(big.NewInt(solanaNativeFeeLamports), 9) || nine.NativeDecimals() != 9 {
		t.Fatalf("9-decimal row: %+v err %v", estimate, err)
	}
	three := NewSolanaLive(SolanaConfig{NativeSymbol: "SOL", NativeDecimal: 3})
	estimate, err = three.EstimateFee(context.Background(), types.TransferRequest{})
	if err != nil || estimate.Fee != amount.FormatBaseUnits(big.NewInt(solanaNativeFeeLamports), 3) {
		t.Fatalf("3-decimal row: %+v err %v", estimate, err)
	}
}
