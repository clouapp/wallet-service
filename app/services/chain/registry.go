package chain

import (
	"fmt"
	"sync"

	"github.com/macromarkets/vault/pkg/types"
)

// ---------------------------------------------------------------------------
// Registry — all chains and tokens. Singleton per Lambda instance.
// ---------------------------------------------------------------------------

type Registry struct {
	mu     sync.RWMutex
	chains map[string]types.Chain
	tokens map[string][]types.Token
}

func NewRegistry() *Registry {
	return &Registry{
		chains: make(map[string]types.Chain),
		tokens: make(map[string][]types.Token),
	}
}

func (r *Registry) RegisterChain(c types.Chain) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chains[c.ID()] = c
}

func (r *Registry) RegisterToken(t types.Token) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokens[t.ChainID] = append(r.tokens[t.ChainID], t)
}

func (r *Registry) Chain(id string) (types.Chain, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.chains[id]
	if !ok {
		return nil, fmt.Errorf("chain not registered: %s", id)
	}
	return c, nil
}

func (r *Registry) ChainIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.chains))
	for id := range r.chains {
		ids = append(ids, id)
	}
	return ids
}

func (r *Registry) TokensForChain(chainID string) []types.Token {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tokens[chainID]
}

func (r *Registry) FindToken(chainID, symbol string) (*types.Token, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, t := range r.tokens[chainID] {
		if t.Symbol == symbol {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("token %s not found on chain %s", symbol, chainID)
}

// ---------------------------------------------------------------------------
// AllTokens — single source of truth for all supported tokens.
// Adding a token = add a line here.
// ---------------------------------------------------------------------------

func AllTokens() []types.Token {
	return []types.Token{
		// Ethereum
		{Symbol: "usdt", Name: "Tether USD", Contract: "0xdAC17F958D2ee523a2206206994597C13D831ec7", Decimals: 6, ChainID: "eth"},
		{Symbol: "usdc", Name: "USD Coin", Contract: "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", Decimals: 6, ChainID: "eth"},
		// Polygon
		{Symbol: "usdt", Name: "Tether USD", Contract: "0xc2132D05D31c914a87C6611C10748AEb04B58e8F", Decimals: 6, ChainID: "polygon"},
		{Symbol: "usdc", Name: "USD Coin", Contract: "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359", Decimals: 6, ChainID: "polygon"},
		// Solana
		{Symbol: "usdc", Name: "USD Coin", Contract: "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", Decimals: 6, ChainID: "sol"},
		{Symbol: "usdt", Name: "Tether USD", Contract: "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB", Decimals: 6, ChainID: "sol"},
	}
}
