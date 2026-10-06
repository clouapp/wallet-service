package chain

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/macrowallets/waas/pkg/types"
)

// ErrUnknownChain is a chain id the registry does not know.
// Callers match chainregistry.ErrUnknownChain, which is this value.
var ErrUnknownChain = errors.New("unknown chain")

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

// ResetCatalog drops every loaded chain and token so a refresh can install the current rows.
func (r *Registry) ResetCatalog() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chains = make(map[string]types.Chain)
	r.tokens = make(map[string][]types.Token)
}

func (r *Registry) RegisterChain(c types.Chain) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chains[c.ID()] = c
}

// endpointReplacer is a live adapter whose next dial can change.
type endpointReplacer interface {
	ReplaceEndpoint(endpoint string)
}

// ReplaceEndpoint points the loaded dialer for chainID at endpoint. A chain
// that was not loaded returns false. The endpoint is not logged. An empty
// endpoint is refused and does not wipe the current one.
func (r *Registry) ReplaceEndpoint(chainID, endpoint string) (bool, error) {
	if r == nil {
		return false, fmt.Errorf("replace chain endpoint: registry is required")
	}
	chainID = strings.TrimSpace(chainID)
	endpoint = strings.TrimSpace(endpoint)
	if chainID == "" || endpoint == "" {
		return false, fmt.Errorf("replace chain endpoint: chain and endpoint are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.chains[chainID]
	if !ok {
		return false, nil
	}
	replacer, ok := current.(endpointReplacer)
	if !ok {
		return false, fmt.Errorf("replace chain endpoint: chain cannot be retargeted")
	}
	replacer.ReplaceEndpoint(endpoint)
	return true, nil
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
		return nil, fmt.Errorf("%w: %s", ErrUnknownChain, id)
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

// FindTokenByContract returns the seeded token for this chain.
// EVM contracts match case-insensitively. Solana mints match exactly.
func (r *Registry) FindTokenByContract(chainID, contract string) (*types.Token, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	want := strings.TrimSpace(contract)
	evm := strings.HasPrefix(strings.ToLower(want), "0x")
	for _, t := range r.tokens[chainID] {
		if evm {
			if strings.EqualFold(t.Contract, want) {
				copy := t
				return &copy, nil
			}
			continue
		}
		if t.Contract == want {
			copy := t
			return &copy, nil
		}
	}
	return nil, fmt.Errorf("token contract %s not found on chain %s", contract, chainID)
}
