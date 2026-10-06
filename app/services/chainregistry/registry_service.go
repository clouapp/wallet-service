package chainregistry

import (
	"context"
	"errors"
	"sync"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// catalogCacheKey is the sealed chain catalog. It follows the vault: prefix
// used by other process caches and is never a settings key.
const catalogCacheKey = "vault:chain-registry"

// errCatalogNotLoaded means Refresh has not stored the chain catalog yet.
var errCatalogNotLoaded = errors.New("chain registry catalog was not loaded")

// ActiveChainStore reads the chains rows, including network columns and the sealed rpc_url.
type ActiveChainStore interface {
	FindActive(ctx context.Context) ([]models.Chain, error)
}

// CatalogCache stores the sealed chain catalog. A miss is found == false.
type CatalogCache interface {
	Get(key string) (rows []models.Chain, found bool)
	Put(key string, rows []models.Chain)
	Forget(key string)
}

// CatalogInstaller registers sealed chain rows on the registry. It opens each rpc_url and does not log it.
type CatalogInstaller func(reg *chain.Registry, rows []models.Chain, tokensByChain map[string][]types.Token) map[string]string

// ChainRegistryService builds chain.Registry from the cached chain catalog.
type ChainRegistryService struct {
	store    ActiveChainStore
	cache    CatalogCache
	install  CatalogInstaller
	registry *chain.Registry
	mu       sync.Mutex
	ready    bool
	networks map[string]string
}

// ChainRegistryDeps is the store, cache, installer, and registry. Store, Install, and Registry are required.
type ChainRegistryDeps struct {
	Store    ActiveChainStore
	Cache    CatalogCache
	Install  CatalogInstaller
	Registry *chain.Registry
}

// NewChainRegistryService builds the registry service. A nil cache uses the in-process catalog cache.
func NewChainRegistryService(deps ChainRegistryDeps) *ChainRegistryService {
	if deps.Store == nil {
		panic("chain registry service: store is required")
	}
	if deps.Install == nil {
		panic("chain registry service: installer is required")
	}
	if deps.Registry == nil {
		panic("chain registry service: registry is required")
	}
	cache := deps.Cache
	if cache == nil {
		cache = NewCatalogCache()
	}
	return &ChainRegistryService{
		store:    deps.Store,
		cache:    cache,
		install:  deps.Install,
		registry: deps.Registry,
		networks: map[string]string{},
	}
}

// NewCatalogCache returns an in-process cache for the sealed chain catalog.
func NewCatalogCache() CatalogCache {
	return &memoryCatalogCache{
		has:  map[string]bool{},
		rows: map[string][]models.Chain{},
	}
}

// Registry returns the chain registry this service fills.
func (s *ChainRegistryService) Registry() *chain.Registry {
	if s == nil {
		return nil
	}
	return s.registry
}

// Networks returns the network name of each chain installed by the last build.
func (s *ChainRegistryService) Networks() map[string]string {
	if s == nil {
		return map[string]string{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.networks))
	for id, name := range s.networks {
		out[id] = name
	}
	return out
}

// Load installs the registry from the cached catalog. It does not query the database.
func (s *ChainRegistryService) Load(tokensByChain map[string][]types.Token) error {
	if s == nil || s.cache == nil {
		return errCatalogNotLoaded
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ready {
		return nil
	}
	rows, found := s.cache.Get(catalogCacheKey)
	if !found {
		return errCatalogNotLoaded
	}
	s.networks = s.rebuild(rows, tokensByChain)
	s.ready = true
	return nil
}

// Refresh reads the chain catalog, stores the sealed rows, and rebuilds the registry from that cache.
func (s *ChainRegistryService) Refresh(ctx context.Context, tokensByChain map[string][]types.Token) error {
	if s == nil || s.store == nil || s.cache == nil {
		return errCatalogNotLoaded
	}
	if ctx == nil {
		return errors.New("chain registry refresh: context is required")
	}
	rows, err := s.store.FindActive(ctx)
	if err != nil {
		s.mu.Lock()
		first := !s.ready
		s.mu.Unlock()
		if first {
			s.cache.Put(catalogCacheKey, []models.Chain{})
			s.mu.Lock()
			s.networks = s.rebuild(nil, tokensByChain)
			s.ready = true
			s.mu.Unlock()
		}
		return err
	}
	s.cache.Forget(catalogCacheKey)
	s.cache.Put(catalogCacheKey, cloneChains(rows))
	s.mu.Lock()
	defer s.mu.Unlock()
	cached, found := s.cache.Get(catalogCacheKey)
	if !found {
		return errCatalogNotLoaded
	}
	s.networks = s.rebuild(cached, tokensByChain)
	s.ready = true
	return nil
}

func (s *ChainRegistryService) rebuild(rows []models.Chain, tokensByChain map[string][]types.Token) map[string]string {
	s.registry.ResetCatalog()
	for _, tokens := range tokensByChain {
		for _, tok := range tokens {
			s.registry.RegisterToken(tok)
		}
	}
	networks := s.install(s.registry, cloneChains(rows), tokensByChain)
	if networks == nil {
		return map[string]string{}
	}
	return networks
}

func cloneChains(rows []models.Chain) []models.Chain {
	out := make([]models.Chain, len(rows))
	copy(out, rows)
	return out
}

type memoryCatalogCache struct {
	mu   sync.Mutex
	has  map[string]bool
	rows map[string][]models.Chain
}

func (c *memoryCatalogCache) Get(key string) ([]models.Chain, bool) {
	if c == nil || key == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rows, found := c.rows[key]
	if !c.has[key] || !found {
		return nil, false
	}
	return cloneChains(rows), true
}

func (c *memoryCatalogCache) Put(key string, rows []models.Chain) {
	if c == nil || key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rows[key] = cloneChains(rows)
	c.has[key] = true
}

func (c *memoryCatalogCache) Forget(key string) {
	if c == nil || key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.rows, key)
	delete(c.has, key)
}
