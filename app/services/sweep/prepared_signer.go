package sweep

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/pkg/mpcshare"
	"github.com/macrowallets/waas/pkg/types"
)

// PreparedSigner keeps MPC material inside custody when signing a transaction
// prepared by the registered adapter. Callers must persist before broadcasting.
type PreparedSigner interface {
	SignPrepared(context.Context, uuid.UUID, string, types.Chain, *types.UnsignedTx) (*types.SignedTx, error)
	PrepareBroadcast(context.Context, types.Chain, *types.SignedTx) (string, error)
}

// PrepareBroadcast seals the signed payload and captures chain reconciliation
// metadata. A swap cannot be sent if recoverable bytes cannot be stored.
func (s *service) PrepareBroadcast(ctx context.Context, adapter types.Chain, signed *types.SignedTx) (string, error) {
	if s.sealer == nil || signed == nil || signed.TxHash == "" || len(signed.RawBytes) == 0 {
		return "", fmt.Errorf("prepared signing: recoverable signed transaction required")
	}
	return s.pendingPayload(adapter, signed, s.broadcastHead(ctx, adapter, signed.ChainID))
}

func (s *service) SignPrepared(ctx context.Context, walletID uuid.UUID, passphrase string, adapter types.Chain, unsigned *types.UnsignedTx) (*types.SignedTx, error) {
	defer mpcshare.DiscardPassphrase(&passphrase)
	if adapter == nil || unsigned == nil || unsigned.ChainID != adapter.ID() {
		return nil, fmt.Errorf("prepared signing: chain mismatch")
	}
	session, err := s.openPreflightSession(ctx, walletID, unsigned.ChainID, passphrase)
	if err != nil {
		return nil, err
	}
	defer session.close()
	if session.wallet.DepositAddress == nil {
		return nil, fmt.Errorf("prepared signing: no base address")
	}
	return s.signUnsigned(ctx, adapter, session.curve, session.keys, session.wallet, *session.wallet.DepositAddress, unsigned)
}
