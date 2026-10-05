package solana

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/gagliardetto/solana-go"

	"github.com/macrowallets/waas/pkg/types"
)

func (a *SolanaLive) GetTokenBalance(ctx context.Context, address string, token types.Token) (*types.Balance, error) {
	owner, err := solana.PublicKeyFromBase58(address)
	if err != nil {
		return nil, fmt.Errorf("sol owner: %w", err)
	}
	mint, err := solana.PublicKeyFromBase58(token.Contract)
	if err != nil {
		return nil, fmt.Errorf("sol mint: %w", err)
	}
	ata, _, err := solana.FindAssociatedTokenAddress(owner, mint)
	if err != nil {
		return nil, fmt.Errorf("sol ata: %w", err)
	}
	var result struct {
		Value struct {
			Amount   string `json:"amount"`
			Decimals uint8  `json:"decimals"`
		} `json:"value"`
	}
	callErr := a.rpc.Call(ctx, "getTokenAccountBalance", &result, ata.String(), map[string]string{"commitment": "finalized"})
	if callErr != nil {
		if strings.Contains(strings.ToLower(callErr.Error()), "could not find account") {
			return &types.Balance{Address: address, Asset: token.Symbol, Amount: big.NewInt(0), Decimals: token.Decimals, Human: "0"}, nil
		}
		return nil, callErr
	}
	amt, ok := new(big.Int).SetString(result.Value.Amount, 10)
	if !ok {
		return nil, fmt.Errorf("sol token amount %q", result.Value.Amount)
	}
	dec := token.Decimals
	if dec == 0 {
		dec = result.Value.Decimals
	}
	return &types.Balance{Address: address, Asset: token.Symbol, Amount: amt, Decimals: dec, Human: fmtUnits(amt, dec)}, nil
}
