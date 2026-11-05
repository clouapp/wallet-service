package postal

import (
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/support/codes"
)

// Factory resolves a provider code to its adapter. It holds no
// configuration — enabling and ordering are the router's business — so the
// factory stays a pure lookup that unit tests can build directly.
type Factory struct {
	adapters codes.Map[LookupAdapter]
}

// NewFactory registers the given adapters under their own codes; see
// codes.Index for duplicates and nils.
func NewFactory(adapters ...LookupAdapter) *Factory {
	return &Factory{adapters: codes.Index(LookupAdapter.Code, adapters...)}
}

func (f *Factory) Get(code ProviderCode) (LookupAdapter, error) {
	adapter, ok := f.adapters[code]
	if !ok {
		return nil, fmt.Errorf("provider %q: %w", code, ErrProviderNotImplemented)
	}
	return adapter, nil
}
