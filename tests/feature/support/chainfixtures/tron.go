// Package chainfixtures builds production chain adapters for service tests that
// run a service against a fake node. Services may not import app/adapters, so the
// adapter is wired here, in the harness, and the service test sees a types.Chain.
package chainfixtures

import (
	"context"

	tronchain "github.com/macrowallets/waas/app/adapters/chain/tron"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// TronNileConfirmations is the confirmation depth of the Nile fixture.
const TronNileConfirmations = 20

// TronNile is the production TRON adapter on Nile whose broadcast goes to a hook
// instead of the network, so a test can record it and mark it included.
type TronNile struct {
	*tronchain.TronLive
	onBroadcast func(ctx context.Context, signed *types.SignedTx) (string, error)
}

// NewTronNile builds the adapter against rpcURL (a fake node) with the given
// registered TRC-20 tokens. onBroadcast replaces BroadcastTransaction.
func NewTronNile(rpcURL string, tokens []types.Token, onBroadcast func(ctx context.Context, signed *types.SignedTx) (string, error)) *TronNile {
	live := tronchain.NewTronLive(tronchain.TronConfig{
		ChainIDStr: models.ChainTron, ChainName: "TRON Nile", NativeSymbol: models.NativeTRX,
		RPCURL: rpcURL, IsTestnet: true, Confirmations: TronNileConfirmations, Tokens: tokens,
	})
	return &TronNile{TronLive: live, onBroadcast: onBroadcast}
}

// BroadcastTransaction hands the signed transaction to the test's hook.
func (c *TronNile) BroadcastTransaction(ctx context.Context, signed *types.SignedTx) (string, error) {
	return c.onBroadcast(ctx, signed)
}
