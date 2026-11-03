package ramp

import (
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/codes"
)

// stub is the marker a placeholder adapter carries. It is satisfied by
// embedding NotImplemented.
type stub interface {
	IsStub() bool
}

// Factory resolves a provider code to its adapter. It holds no configuration:
// enabling, credentials and eligibility are the router's business, so the
// factory stays a pure lookup that unit tests can build with mocks.
type Factory struct {
	adapters codes.Map[ProviderAdapter]
}

// NewFactory registers the given adapters under their own codes; see
// codes.Index for duplicates and nils.
func NewFactory(adapters ...ProviderAdapter) *Factory {
	return &Factory{adapters: codes.Index(ProviderAdapter.Code, adapters...)}
}

// Replace overwrites the adapter registered for code. Production never calls
// it — adapters are fixed at boot — it exists for feature tests that need to
// substitute a decorator (a spy, a recorder) for a real adapter, the same way
// NewFactory's own override-by-code semantics let test setup register one
// provider at construction time.
func (f *Factory) Replace(code ProviderCode, adapter ProviderAdapter) {
	f.adapters[code] = adapter
}

// Get returns the adapter for code. A registered stub is returned as-is: it
// satisfies the contract and refuses each operation with
// ErrProviderNotImplemented, which is what a caller reaching past the router
// should see. An unregistered code gets the same error, since from the
// caller's side the difference is not actionable.
func (f *Factory) Get(code ProviderCode) (ProviderAdapter, error) {
	adapter, ok := f.adapters[code]
	if !ok {
		return nil, fmt.Errorf("provider %q: %w", code, ErrProviderNotImplemented)
	}
	return adapter, nil
}

// Codes returns every registered provider code, sorted, so a sweep over "all
// providers" has a deterministic order and does not name one in its own source.
//
// It is not an eligibility answer: a code here may be a stub or a disabled
// provider. A caller walking it either asks the factory for the port it needs or
// finds no rows for that provider and moves on.
func (f *Factory) Codes() []ProviderCode {
	return f.adapters.Keys()
}

// IsImplemented reports whether the adapter behind code does real work. The
// router calls this to skip a stub before making any request — a stub must
// never consume a position in a failover chain.
func (f *Factory) IsImplemented(code ProviderCode) bool {
	adapter, ok := f.adapters[code]
	if !ok {
		return false
	}
	if marker, ok := adapter.(stub); ok && marker.IsStub() {
		return false
	}
	return true
}

// ValidateMode reports whether a provider's mode is coherent with the
// application environment.
//
// Both directions are errors, and both are the same class of accident.
// Production credentials outside production move real money on a developer's
// machine; sandbox credentials inside production hand real users orders that
// will never settle.
func ValidateMode(mode, appEnv string) error {
	switch mode {
	case models.ProviderModeSandbox, models.ProviderModeProduction:
	default:
		return fmt.Errorf("provider mode %q is not %q or %q", mode, models.ProviderModeSandbox, models.ProviderModeProduction)
	}
	if isProductionEnv := appEnv == productionEnv; isProductionEnv != (mode == models.ProviderModeProduction) {
		return fmt.Errorf("provider mode %q does not match APP_ENV %q", mode, appEnv)
	}
	return nil
}

const productionEnv = "production"
