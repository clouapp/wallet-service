package sweep

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/types"
)

// EVMCallPreflighter signs a fully built EVM call from a wallet's base address on
// any EVM network the same key controls (e.g. an L1 bridge deposit for an L2
// wallet). Like the other preflights it broadcasts, persists and locks nothing;
// the signed bytes go back to the caller, verified against the base address.
type EVMCallPreflighter interface {
	PreflightEVMCall(ctx context.Context, walletID uuid.UUID, passphrase string, adapter types.Chain, unsigned *types.UnsignedTx) (*types.SignedTx, error)
}

var _ EVMCallPreflighter = (*service)(nil)

func (s *service) PreflightEVMCall(ctx context.Context, walletID uuid.UUID, passphrase string, adapter types.Chain, unsigned *types.UnsignedTx) (*types.SignedTx, error) {
	if adapter == nil {
		return nil, fmt.Errorf("sweep preflight: evm call adapter is required")
	}
	if unsigned == nil || len(unsigned.RawBytes) == 0 {
		return nil, fmt.Errorf("sweep preflight: evm call must be built before signing")
	}
	if adapter.ID() != unsigned.ChainID {
		return nil, fmt.Errorf("sweep preflight: evm call built for %s, adapter is %s", unsigned.ChainID, adapter.ID())
	}

	session, err := s.openPreflightSession(ctx, walletID, unsigned.ChainID, passphrase)
	if err != nil {
		return nil, err
	}
	defer session.close()

	if session.curve != mpcpkg.CurveSecp256k1 {
		return nil, fmt.Errorf("sweep preflight: wallet %s uses %s; evm calls need secp256k1", walletID, session.curve)
	}
	return s.signUnsigned(ctx, adapter, session.curve, session.keys, session.wallet, *session.wallet.DepositAddress, unsigned)
}
