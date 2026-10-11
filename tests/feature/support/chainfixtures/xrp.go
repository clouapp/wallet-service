package chainfixtures

import (
	"context"

	xrpchain "github.com/macrowallets/waas/app/adapters/chain/xrp"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// XRPAltnetConfirmations is the confirmation depth of the altnet fixture.
const XRPAltnetConfirmations = 1

// XRPAltnet is the production XRP Ledger adapter on the altnet whose broadcast goes
// to a hook instead of the network, so a test can record it.
type XRPAltnet struct {
	*xrpchain.Live
	onBroadcast func(ctx context.Context, signed *types.SignedTx) (string, error)
}

// NewXRPAltnet builds the txrp adapter against rpcURL (a fake rippled).
// onBroadcast replaces BroadcastTransaction.
func NewXRPAltnet(rpcURL string, onBroadcast func(ctx context.Context, signed *types.SignedTx) (string, error)) *XRPAltnet {
	live := xrpchain.NewLive(xrpchain.Config{
		ChainIDStr: models.ChainTXRP, ChainName: "XRP Ledger Testnet", NativeSymbol: models.NativeXRP,
		RPCURL: rpcURL, IsTestnet: true, Confirmations: XRPAltnetConfirmations,
	})
	return &XRPAltnet{Live: live, onBroadcast: onBroadcast}
}

// BroadcastTransaction hands the signed payment to the test's hook.
func (c *XRPAltnet) BroadcastTransaction(ctx context.Context, signed *types.SignedTx) (string, error) {
	return c.onBroadcast(ctx, signed)
}
