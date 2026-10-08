package withdraw

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/macrowallets/waas/pkg/types"
)

// ErrUnknownAsset: the asset is neither the chain's native coin nor a seeded token.
var ErrUnknownAsset = errors.New("unknown asset")

type ResolvedWithdrawal struct {
	WalletAsset string
	BaseUnits   *big.Int
	Token       *types.Token
}

// ResolvedAsset is a withdrawable asset of a chain; Token is nil for the native coin.
type ResolvedAsset struct {
	WalletAsset string
	Decimals    int
	Token       *types.Token
}

// ResolveWithdrawalAmount converts a human amount into base units.
// Empty asset means the chain native asset. Any other asset must match a
// seeded token symbol, case-insensitively, and uses that token's decimals.
// Polygon native is POL; the legacy MATIC ticker is accepted as an alias.
func ResolveWithdrawalAmount(chainID, nativeSymbol string, nativeDecimals int, requested, human string, tokens []types.Token) (*ResolvedWithdrawal, error) {
	asset, err := ResolveAsset(chainID, nativeSymbol, nativeDecimals, requested, tokens)
	if err != nil {
		return nil, err
	}
	base, err := ParseHumanAmount(human, asset.Decimals)
	if err != nil {
		return nil, err
	}
	return &ResolvedWithdrawal{WalletAsset: asset.WalletAsset, BaseUnits: base, Token: asset.Token}, nil
}

// ResolveAsset applies ResolveWithdrawalAmount's asset rules without an amount.
func ResolveAsset(chainID, nativeSymbol string, nativeDecimals int, requested string, tokens []types.Token) (ResolvedAsset, error) {
	req := strings.TrimSpace(requested)
	if req == "" || types.SameAssetSymbol(req, nativeSymbol) {
		return ResolvedAsset{WalletAsset: nativeSymbol, Decimals: nativeDecimals}, nil
	}
	for i := range tokens {
		if strings.EqualFold(tokens[i].Symbol, req) {
			token := tokens[i]
			return ResolvedAsset{WalletAsset: token.Symbol, Decimals: int(token.Decimals), Token: &token}, nil
		}
	}
	return ResolvedAsset{}, fmt.Errorf("%w %s on chain %s", ErrUnknownAsset, req, chainID)
}

// ParseHumanAmount converts a positive human decimal amount into base units.
func ParseHumanAmount(human string, decimals int) (*big.Int, error) {
	return humanToBaseUnits(human, decimals)
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
