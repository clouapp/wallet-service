package chain

import (
	"fmt"
	"sync"

	"github.com/macrowallets/waas/pkg/types"
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
