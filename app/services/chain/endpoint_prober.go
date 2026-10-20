package chain

import (
	"context"
	"strings"
	"sync"
)

// EndpointProber asks one endpoint of a chain whose adapter speaks its own protocol
// (TRON, XRP) which network it serves, as the token the adapter compares (the TRON
// block 0 id, the XRP Ledger network_id), and its head. The adapter registers it;
// the endpoint checks of chains:rpc-endpoints and the platform routes ask through
// it. Neither the URL nor a key is in an error.
type EndpointProber interface {
	Network(ctx context.Context, rawURL string) (string, error)
	Head(ctx context.Context, rawURL string) (uint64, error)
}

var (
	endpointProbersMu sync.RWMutex
	endpointProbers   = map[string]EndpointProber{}
)

// SetEndpointProber registers the prober of adapterType; nil removes it.
func SetEndpointProber(adapterType string, prober EndpointProber) {
	endpointProbersMu.Lock()
	defer endpointProbersMu.Unlock()
	adapterType = strings.TrimSpace(adapterType)
	if prober == nil {
		delete(endpointProbers, adapterType)
		return
	}
	endpointProbers[adapterType] = prober
}

// EndpointProberOf is the prober of adapterType, or nil when its adapter is not
// linked into the process.
func EndpointProberOf(adapterType string) EndpointProber {
	endpointProbersMu.RLock()
	defer endpointProbersMu.RUnlock()
	return endpointProbers[strings.TrimSpace(adapterType)]
}
