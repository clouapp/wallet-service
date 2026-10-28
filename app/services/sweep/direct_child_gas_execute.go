package sweep

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/types"
)

func (s *service) executeDirectChildGasPreparation(ctx context.Context, plan *Plan, creds SigningCredentials, to string) error {
	if plan.GasPreparationBinding == nil || plan.SourceAddress == nil || plan.WithdrawalID == uuid.Nil || plan.Strategy != StrategyDirectFromChild {
		return ErrGasPreparationBinding
	}
	b := *plan.GasPreparationBinding
	if b.OperationID == uuid.Nil {
		b.OperationID, b.ClaimID = plan.WithdrawalID, plan.WithdrawalID
	}
	if b.OperationID != plan.WithdrawalID || b.ClaimID != b.OperationID || b.WalletID != plan.WalletID || b.Chain != plan.Chain || b.SourceAddressID != plan.SourceAddress.ID || b.AmountRaw != plan.Amount.String() || !strings.EqualFold(b.Asset, plan.Asset) || !strings.EqualFold(b.Recipient, to) {
		return ErrGasPreparationBinding
	}
	var result *DirectChildGasResult
	var err error
	if plan.gasPreparationExisting {
		result, err = s.ResumeDirectChildGas(ctx, b)
	} else {
		result, err = s.prepareDirectChildGas(ctx, b, *plan.GasPreparation, func(signCtx context.Context, g *directGasContext, unsigned *types.UnsignedTx) (*types.SignedTx, error) {
			shareB, shareErr := s.fetchShareB(signCtx, g.wallet)
			if shareErr != nil {
				return nil, shareErr
			}
			defer zeroBytes(shareB)
			keys := walletKeys{shareA: creds.ShareA, shareB: shareB, passphrase: creds.Passphrase}
			return s.signUnsigned(signCtx, g.adapter, mpcpkg.Curve(g.wallet.MPCCurve), keys, g.wallet, *g.wallet.DepositAddress, unsigned)
		})
	}
	if err != nil {
		return fmt.Errorf("%w: gas preparation: %w", ErrSendUnverified, err)
	}
	if result == nil || !result.Confirmed {
		return fmt.Errorf("%w: %w", ErrSendUnverified, ErrGasPreparationPending)
	}
	return nil
}
