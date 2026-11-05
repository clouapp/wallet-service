package mailer

import (
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/support/codes"
)

// Factory resolves a driver to its transport. It holds no configuration:
// credentials are the Config's business, so the factory stays a pure lookup
// that unit tests can build with fakes.
type Factory struct{ transports codes.Map[Transport] }

// NewFactory registers the given transports under their own driver names; see
// codes.Index for duplicates and nils.
func NewFactory(transports ...Transport) *Factory {
	return &Factory{transports: codes.Index(Transport.Driver, transports...)}
}

func (f *Factory) Get(driver string) (Transport, error) {
	transport, ok := f.transports[driver]
	if !ok {
		return nil, fmt.Errorf("mailer: driver %q: %w", driver, ErrUnknownDriver)
	}
	return transport, nil
}
