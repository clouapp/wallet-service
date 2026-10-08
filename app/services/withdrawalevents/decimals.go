package withdrawalevents

import (
	"context"
	"strings"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

type TokenRegistry interface {
	TokensForChain(chainID string) []types.Token
	Chain(chainID string) (types.Chain, error)
}

type ChainStore interface {
	FindByID(ctx context.Context, id string) (*models.Chain, error)
}

// RegistryDecimals resolves decimals from the seeded tokens and the chain's native decimals.
type RegistryDecimals struct {
	registry TokenRegistry
	chains   ChainStore
}

// RegistryDecimalsDeps is everything the decimal lookup needs. A nil field means that
// dependency is absent.
type RegistryDecimalsDeps struct {
	Registry TokenRegistry
	Chains   ChainStore
}

// NewRegistryDecimals wires the decimal lookup from RegistryDecimalsDeps.
func NewRegistryDecimals(deps RegistryDecimalsDeps) RegistryDecimals {
	return RegistryDecimals{registry: deps.Registry, chains: deps.Chains}
}

func (r RegistryDecimals) Decimals(chainID, asset string) (int, bool) {
	if r.registry == nil || strings.TrimSpace(asset) == "" {
		return 0, false
	}
	for _, token := range r.registry.TokensForChain(chainID) {
		if strings.EqualFold(token.Symbol, asset) {
			return int(token.Decimals), true
		}
	}

	adapter, err := r.registry.Chain(chainID)
	if err != nil || adapter == nil || !types.SameAssetSymbol(adapter.NativeAsset(), asset) || r.chains == nil {
		return 0, false
	}
	chain, err := r.chains.FindByID(context.Background(), chainID)
	if err != nil || chain == nil {
		return 0, false
	}
	return chain.NativeDecimals, true
}
