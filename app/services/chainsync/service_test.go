package chainsync

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

type keyedAdapter struct {
	types.Chain
	key string
}

func (a *keyedAdapter) SetAPIKey(key string) { a.key = key }

type fakeRegistry struct {
	mu       sync.Mutex
	ids      []string
	adapters map[string]types.Chain
	replaced map[string]EndpointList
	single   map[string]string
	refuse   map[string]bool
}

func (r *fakeRegistry) ChainIDs() []string { return r.ids }

func (r *fakeRegistry) Chain(id string) (types.Chain, error) {
	if adapter, ok := r.adapters[id]; ok {
		return adapter, nil
	}
	return nil, errors.New("unknown chain")
}

func (r *fakeRegistry) ReplaceEndpoint(chainID, endpoint string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.single[chainID] = endpoint
	return true, nil
}

func (r *fakeRegistry) ReplaceEndpoints(chainID, primary string, fallbacks []chain.RPCFallback) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.refuse[chainID] {
		return false, errors.New("chain takes a single endpoint")
	}
	r.replaced[chainID] = EndpointList{Primary: primary, Fallbacks: fallbacks}
	return true, nil
}

func newFakeRegistry(ids ...string) *fakeRegistry {
	return &fakeRegistry{ids: ids, adapters: map[string]types.Chain{}, replaced: map[string]EndpointList{}, single: map[string]string{}, refuse: map[string]bool{}}
}

// TestPass_MovesOnlyAChainWhoseStoredListChangedAfterTheFirstPass: the first pass
// records what the registry was built from, a later one replaces the list of the
// chain that changed and leaves the others; an adapter that keeps its own failover
// takes its primary only.
func TestPass_MovesOnlyAChainWhoseStoredListChangedAfterTheFirstPass(t *testing.T) {
	registry := newFakeRegistry("eth", "sol", "btc")
	registry.refuse["btc"] = true
	lists := map[string]EndpointList{
		"eth": {Primary: "https://eth-a.example", Fallbacks: []chain.RPCFallback{{URL: "https://eth-b.example", Position: 2}}},
		"sol": {Primary: "https://sol-a.example"},
		"btc": {Primary: "https://btc-a.example"},
	}
	sync := New(Deps{Registry: registry, Endpoints: func(context.Context) (map[string]EndpointList, error) { return lists, nil }})

	if moved := sync.Pass(context.Background()); moved != 0 || len(registry.replaced) != 0 {
		t.Fatalf("the first pass must only record: moved %d, replaced %v", moved, registry.replaced)
	}
	if moved := sync.Pass(context.Background()); moved != 0 {
		t.Fatalf("an unchanged list moved %d chains", moved)
	}

	lists = map[string]EndpointList{
		"eth": {Primary: "https://eth-a.example", Fallbacks: []chain.RPCFallback{{URL: "https://eth-c.example", Position: 2}}},
		"sol": {Primary: "https://sol-a.example"},
		"btc": {Primary: "https://btc-b.example"},
	}
	if moved := sync.Pass(context.Background()); moved != 2 {
		t.Fatalf("moved %d chains, want eth and btc", moved)
	}
	if got := registry.replaced["eth"]; got.Fallbacks[0].URL != "https://eth-c.example" {
		t.Fatalf("eth fallbacks = %+v", got)
	}
	if _, ok := registry.replaced["sol"]; ok {
		t.Fatal("an unchanged chain was moved")
	}
	if registry.single["btc"] != "https://btc-b.example" {
		t.Fatalf("btc primary = %q, want the new one", registry.single["btc"])
	}
}

func TestPass_AFailedReadMovesNothingAndTheNextOneApplies(t *testing.T) {
	registry := newFakeRegistry("eth")
	var fail bool
	list := EndpointList{Primary: "https://eth-a.example"}
	sync := New(Deps{Registry: registry, Endpoints: func(context.Context) (map[string]EndpointList, error) {
		if fail {
			return nil, errors.New("db down")
		}
		return map[string]EndpointList{"eth": list}, nil
	}})
	sync.Pass(context.Background())
	fail = true
	list = EndpointList{Primary: "https://eth-b.example"}
	if moved := sync.Pass(context.Background()); moved != 0 {
		t.Fatalf("a failed read moved %d chains", moved)
	}
	fail = false
	if moved := sync.Pass(context.Background()); moved != 1 || registry.replaced["eth"].Primary != "https://eth-b.example" {
		t.Fatalf("the next read must apply the change: moved %d, %+v", moved, registry.replaced["eth"])
	}
}

// TestPass_SetsAChangedTronGridKeyOnEveryAdapterThatTakesOne: the first pass keeps
// the key the adapters were built with; a changed key reaches every TRON adapter.
func TestPass_SetsAChangedTronGridKeyOnEveryAdapterThatTakesOne(t *testing.T) {
	registry := newFakeRegistry("tron", "ttron", "eth")
	tron, ttron := &keyedAdapter{key: "boot"}, &keyedAdapter{key: "boot"}
	registry.adapters["tron"], registry.adapters["ttron"] = tron, ttron
	key := "boot"
	sync := New(Deps{
		Registry:   registry,
		Endpoints:  func(context.Context) (map[string]EndpointList, error) { return nil, nil },
		TronAPIKey: func(context.Context) string { return key },
	})
	sync.Pass(context.Background())
	if tron.key != "boot" {
		t.Fatalf("the first pass changed the key to %q", tron.key)
	}
	key = "panel"
	sync.Pass(context.Background())
	if tron.key != "panel" || ttron.key != "panel" {
		t.Fatalf("keys = %q %q, want panel on both", tron.key, ttron.key)
	}
}
