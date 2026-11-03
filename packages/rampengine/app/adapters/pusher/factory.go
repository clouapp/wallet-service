package pusher

import (
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/support/codes"
)

// Factory resolves a platform to its transport. It holds no configuration:
// credentials are the Config's business, so the factory stays a pure lookup
// that unit tests can build with fakes.
type Factory struct{ transports codes.Map[Transport] }

// NewFactory registers the given transports under their own platforms; see
// codes.Index for duplicates and nils.
func NewFactory(transports ...Transport) *Factory {
	return &Factory{transports: codes.Index(Transport.Platform, transports...)}
}

// Get returns the transport that reaches platform.
func (f *Factory) Get(platform string) (Transport, error) {
	transport, ok := f.transports[platform]
	if !ok {
		return nil, fmt.Errorf("push: platform %q: %w", platform, ErrUnknownPlatform)
	}
	return transport, nil
}
