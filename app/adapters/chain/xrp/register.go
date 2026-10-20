package xrp

import (
	"context"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
)

func init() {
	chain.SetEndpointProber(models.AdapterTypeXRP, endpointProber{})
}

// endpointProber asks a rippled server for its network_id and its validated ledger,
// for the endpoint checks of chains:rpc-endpoints and the platform routes.
type endpointProber struct{}

func (endpointProber) Network(ctx context.Context, rawURL string) (string, error) {
	return NetworkID(ctx, rawURL)
}

func (endpointProber) Head(ctx context.Context, rawURL string) (uint64, error) {
	return NewLive(Config{RPCURL: rawURL}).GetLatestBlock(ctx)
}
