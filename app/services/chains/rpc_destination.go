package chains

import (
	"context"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/egress"
)

// destinationGuard refuses an RPC URL whose destination the RPC policy does not
// allow, by its literal host or by what its name resolves to now (egress.Guard
// with egress.RPCPolicy).
type destinationGuard interface {
	CheckDestination(ctx context.Context, rawURL string) error
}

// defaultRPCDestinations is the RPC policy with link-local and metadata destinations
// refused, over the system resolver: the guard of a writer built without one.
func defaultRPCDestinations() destinationGuard {
	return egress.NewGuard(egress.RPCPolicy(false), nil)
}

// rpcDestination refuses the URL a chain RPC write would dial when destinations
// refuses it: the URL itself, or the value an env:NAME reference names here. A
// reference that does not resolve here is left to the checks that follow. The
// refusal is the policy's sentence and never carries the URL.
func rpcDestination(ctx context.Context, destinations destinationGuard, value string) error {
	if dial, err := models.DialEndpoint(value); err == nil {
		return destinations.CheckDestination(ctx, dial)
	}
	return nil
}
