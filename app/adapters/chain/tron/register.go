package tron

import (
	"context"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
)

func init() {
	chain.SetEndpointProber(models.AdapterTypeTron, endpointProber{})
}

// endpointProber asks a java-tron HTTP endpoint for its block 0 id and its head, for
// the endpoint checks of chains:rpc-endpoints and the platform routes.
type endpointProber struct{}

func (endpointProber) Network(ctx context.Context, rawURL string) (string, error) {
	return GenesisBlockID(ctx, rawURL)
}

func (endpointProber) Head(ctx context.Context, rawURL string) (uint64, error) {
	return NewTronLive(TronConfig{RPCURL: rawURL}).GetLatestBlock(ctx)
}
