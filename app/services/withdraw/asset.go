package withdraw

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/macrowallets/waas/pkg/types"
)

type ResolvedWithdrawal struct {
	WalletAsset string
	BaseUnits   *big.Int
	Token       *types.Token
}

// ResolveWithdrawalAmount converts a human amount into base units.
// Empty asset means the chain native asset. Any other asset must match a
// seeded token symbol, case-insensitively, and uses that token's decimals.
// Polygon native is POL; the legacy MATIC ticker is accepted as an alias.
func ResolveWithdrawalAmount(chainID, nativeSymbol string, nativeDecimals int, requested, human string, tokens []types.Token) (*ResolvedWithdrawal, error) {
	req := strings.TrimSpace(requested)
	if req == "" || types.SameAssetSymbol(req, nativeSymbol) {
		base, err := humanToBaseUnits(human, nativeDecimals)
		if err != nil {
			return nil, err
		}
		return &ResolvedWithdrawal{WalletAsset: nativeSymbol, BaseUnits: base}, nil
	}
	var match *types.Token
	for i := range tokens {
		if strings.EqualFold(tokens[i].Symbol, req) {
			copy := tokens[i]
			match = &copy
			break
		}
	}
	if match == nil {
		return nil, fmt.Errorf("unknown asset %s on chain %s", req, chainID)
	}
	base, err := humanToBaseUnits(human, int(match.Decimals))
	if err != nil {
		return nil, err
	}
	return &ResolvedWithdrawal{WalletAsset: match.Symbol, BaseUnits: base, Token: match}, nil
}

func humanToBaseUnits(human string, decimals int) (*big.Int, error) {
	if decimals < 0 || decimals > 36 {
		return nil, fmt.Errorf("invalid decimals")
	}
	trimmed := strings.TrimSpace(human)
	if trimmed == "" {
		return nil, fmt.Errorf("amount is required")
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) > 2 {
		return nil, fmt.Errorf("invalid amount")
	}
	whole := parts[0]
	if whole == "" {
		whole = "0"
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	if strings.TrimLeft(whole, "0123456789") != "" || strings.TrimLeft(frac, "0123456789") != "" {
		return nil, fmt.Errorf("invalid amount")
	}
	if len(frac) > decimals {
		return nil, fmt.Errorf("amount has too many decimal places")
	}
	frac += strings.Repeat("0", decimals-len(frac))
	combined := strings.TrimLeft(whole+frac, "0")
	if combined == "" {
		return nil, fmt.Errorf("amount must be greater than zero")
	}
	value, ok := new(big.Int).SetString(combined, 10)
	if !ok {
		return nil, fmt.Errorf("invalid amount")
	}
	return value, nil
}
