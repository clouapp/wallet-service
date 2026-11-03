package companyregistry

import (
	"context"
	"errors"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
)

// Router picks the provider for a CNPJ lookup: the admin's configured order,
// narrowed to what is implemented and enabled, as postal.Router does for a
// CEP. A lookup moves no money and holds no state, so there is no per-user
// eligibility.
type Router struct {
	factory *Factory
	config  *Config
}

func NewRouter(factory *Factory, config *Config) *Router {
	return &Router{factory: factory, config: config}
}

// Resolve tries each configured, enabled, implemented provider in order
// until one answers. A provider's ErrNotFound is definitive and returned
// without asking the next one. Any other error moves on to the next
// candidate; when every provider that ran failed, the last failure is
// returned as is. ErrNoProviderAvailable means none ran.
func (r *Router) Resolve(ctx context.Context, cnpj string) (Company, error) {
	order, err := r.config.Order()
	if err != nil {
		return Company{}, err
	}

	var lastErr error
	for _, code := range order {
		adapter, ok := r.candidate(code)
		if !ok {
			continue
		}
		company, err := adapter.Lookup(ctx, cnpj)
		if err == nil {
			return company, nil
		}
		if errors.Is(err, ErrNotFound) {
			return Company{}, err
		}
		lastErr = err
	}
	if lastErr != nil {
		return Company{}, lastErr
	}
	return Company{}, ErrNoProviderAvailable
}

// candidate returns the adapter for code if it is registered and enabled. A
// state that cannot be read is "not a candidate", logged, so a settings
// outage reads apart from a provider an admin turned off.
func (r *Router) candidate(code ProviderCode) (LookupAdapter, bool) {
	adapter, err := r.factory.Get(code)
	if err != nil {
		return nil, false
	}
	state, err := r.config.Provider(code)
	if err != nil {
		facades.Log().Warningf("company registry: read state for %q: %v", code, err)
		return nil, false
	}
	if !state.Enabled {
		return nil, false
	}
	return adapter, true
}
