package postal

import (
	"context"
	"errors"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
)

// Router picks the provider for a CEP lookup. The client never chooses: the
// choice is the intersection of the admin's configured order and what is
// actually implemented and enabled — there is no per-user eligibility here,
// unlike ramp.Router, because a lookup moves no money and holds no state.
type Router struct {
	factory *Factory
	config  *Config
}

func NewRouter(factory *Factory, config *Config) *Router {
	return &Router{factory: factory, config: config}
}

// Resolve tries each configured, enabled, implemented provider in order
// until one answers. A provider's ErrNotFound is definitive and returned
// immediately without trying the next one — a CEP that does not exist at
// ViaCEP does not exist at BrasilAPI either, and asking twice only adds
// latency. Any other error is treated as transport failure and moves on to
// the next candidate.
//
// When every provider that ran failed, the last failure is returned as is, so
// a provider's ErrProviderFailed still reads as one. ErrNoProviderAvailable
// means none ran: every configured provider was disabled or unregistered.
func (r *Router) Resolve(ctx context.Context, postalCode string) (Address, error) {
	order, err := r.config.Order()
	if err != nil {
		return Address{}, err
	}

	var lastErr error
	for _, code := range order {
		adapter, ok := r.candidate(code)
		if !ok {
			continue
		}
		address, err := adapter.Lookup(ctx, postalCode)
		if err == nil {
			return address, nil
		}
		if errors.Is(err, ErrNotFound) {
			return Address{}, err
		}
		lastErr = err
	}
	if lastErr != nil {
		return Address{}, lastErr
	}
	return Address{}, ErrNoProviderAvailable
}

// candidate returns the adapter for code if it is both registered and
// enabled by the admin. A missing Config.Provider read (unregistered
// settings group) is treated as "not a candidate" rather than propagated,
// since a lookup provider always has a safe default — but it is logged, so
// a settings-service outage is distinguishable from a deliberately disabled
// provider instead of silently degrading every lookup.
func (r *Router) candidate(code ProviderCode) (LookupAdapter, bool) {
	adapter, err := r.factory.Get(code)
	if err != nil {
		return nil, false
	}
	state, err := r.config.Provider(code)
	if err != nil {
		facades.Log().Warningf("postal: read state for %q: %v", code, err)
		return nil, false
	}
	if !state.Enabled {
		return nil, false
	}
	return adapter, true
}
