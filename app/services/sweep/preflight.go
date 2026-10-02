package sweep

import (
	"context"
	"fmt"
	"math/big"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/types"
)

// Preflighter builds and signs what a withdrawal or a consolidation would broadcast,
// with the same code and key material, and stops there: nothing is broadcast,
// persisted, locked, or counted against a quota.
type Preflighter interface {
	PreflightWithdrawal(ctx context.Context, plan *Plan, passphrase string, toAddress string) (*Preflight, error)
	PreflightConsolidation(ctx context.Context, walletID uuid.UUID, asset string, passphrase string) (*Preflight, error)
}

// PreflightTx is one signed, verified, never-broadcast transaction.
type PreflightTx struct {
	Role   string
	From   string
	To     string
	Amount *big.Int
	TxHash string
}

// Preflight lists the transactions a plan would broadcast, in order. Every one was
// signed and its signature checked against From before it was added.
type Preflight struct {
	Strategy     Strategy
	Asset        string
	EstimatedGas *big.Int
	Transactions []PreflightTx
}

const (
	preflightRoleWithdrawal = "withdrawal"
	preflightRoleSweep      = "sweep"
	preflightRoleGasSeed    = "gas_seed"
)

var _ Preflighter = (*service)(nil)

func (s *service) PreflightWithdrawal(ctx context.Context, plan *Plan, passphrase string, toAddress string) (*Preflight, error) {
	if plan == nil {
		return nil, fmt.Errorf("sweep preflight: plan must not be nil")
	}
	if toAddress == "" {
		return nil, fmt.Errorf("sweep preflight: toAddress must not be empty")
	}
	switch plan.Strategy {
	case StrategyDirectFromBase, StrategyDirectFromChild:
	case StrategyInsufficient:
		return nil, ErrInsufficientFunds
	default:
		return nil, fmt.Errorf("sweep preflight: strategy %s is not covered; its final transfer spends outputs of earlier legs", plan.Strategy)
	}
	if plan.SourceAddress == nil {
		return nil, fmt.Errorf("sweep preflight: plan source address missing for strategy %s", plan.Strategy)
	}

	session, err := s.openPreflightSession(ctx, plan.WalletID, plan.Chain, passphrase)
	if err != nil {
		return nil, err
	}
	defer session.close()

	signed, _, err := s.signWithdrawalTransfer(ctx, session.adapter, session.curve, session.keys, session.wallet, plan, *plan.SourceAddress, toAddress)
	if err != nil {
		return nil, err
	}
	return &Preflight{
		Strategy:     plan.Strategy,
		Asset:        plan.Asset,
		EstimatedGas: copyBigInt(plan.EstimatedGas),
		Transactions: []PreflightTx{{
			Role:   preflightRoleWithdrawal,
			From:   plan.SourceAddress.Address,
			To:     toAddress,
			Amount: copyBigInt(plan.Amount),
			TxHash: signed.TxHash,
		}},
	}, nil
}

func (s *service) PreflightConsolidation(ctx context.Context, walletID uuid.UUID, asset string, passphrase string) (*Preflight, error) {
	wallet, err := s.walletRepo.FindByID(ctx, walletID)
	if err != nil {
		return nil, fmt.Errorf("sweep preflight: find wallet: %w", err)
	}
	if wallet == nil || wallet.DepositAddress == nil {
		return nil, fmt.Errorf("sweep preflight: wallet %s not found or has no base address", walletID)
	}
	chainEntity, err := s.chainRepo.FindByID(wallet.Chain)
	if err != nil || chainEntity == nil {
		return nil, fmt.Errorf("sweep preflight: chain %q not found: %v", wallet.Chain, err)
	}
	if !supportedSweepAdapter(chainEntity.AdapterType) {
		return nil, ErrUnsupportedChain
	}
	limits, err := s.LoadLimits(ctx, uuid.Nil)
	if err != nil {
		return nil, err
	}

	session, err := s.openPreflightSession(ctx, walletID, wallet.Chain, passphrase)
	if err != nil {
		return nil, err
	}
	defer session.close()

	plan, err := s.planConsolidation(ctx, session.adapter, session.wallet, chainEntity.AdapterType, asset, limits)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return &Preflight{Strategy: StrategyMultiSweep, Asset: asset}, nil
	}

	preflight := &Preflight{Strategy: plan.Strategy, Asset: plan.Asset, EstimatedGas: copyBigInt(plan.EstimatedGas)}
	for _, leg := range plan.Sweeps {
		legTxs, err := s.preflightSweepLeg(ctx, session, plan, leg)
		if err != nil {
			return nil, fmt.Errorf("sweep preflight: leg from %s: %w", leg.From.Address, err)
		}
		preflight.Transactions = append(preflight.Transactions, legTxs...)
	}
	return preflight, nil
}

func (s *service) preflightSweepLeg(ctx context.Context, session *preflightSession, plan *Plan, leg PlannedSweep) ([]PreflightTx, error) {
	unsigneds, _, err := s.buildSweepLeg(ctx, session.adapter, session.wallet, plan, leg)
	if err != nil {
		return nil, err
	}
	signedTxs := make([]PreflightTx, 0, len(unsigneds))
	for idx := range unsigneds {
		signer, isGasSeed := sweepLegSigner(session.wallet, leg, len(unsigneds), idx)
		signed, err := s.signUnsigned(ctx, session.adapter, session.curve, session.keys, session.wallet, signer, &unsigneds[idx])
		if err != nil {
			return nil, fmt.Errorf("sign transaction: %w", err)
		}
		entry := PreflightTx{Role: preflightRoleSweep, From: leg.From.Address, To: session.wallet.DepositAddress.Address, Amount: copyBigInt(leg.Amount), TxHash: signed.TxHash}
		if isGasSeed {
			entry = PreflightTx{Role: preflightRoleGasSeed, From: signer.Address, To: leg.From.Address, TxHash: signed.TxHash}
		}
		signedTxs = append(signedTxs, entry)
	}
	return signedTxs, nil
}

// preflightSession holds the wallet, adapter, and both shares for one preflight;
// close zeroes the shares.
type preflightSession struct {
	wallet  *models.Wallet
	adapter types.Chain
	curve   mpcpkg.Curve
	keys    walletKeys
}

func (s *service) openPreflightSession(ctx context.Context, walletID uuid.UUID, chainID string, passphrase string) (*preflightSession, error) {
	if len(passphrase) < 12 {
		return nil, fmt.Errorf("passphrase must be at least 12 characters")
	}
	wallet, err := s.walletRepo.FindByID(ctx, walletID)
	if err != nil {
		return nil, fmt.Errorf("sweep preflight: find wallet: %w", err)
	}
	if wallet == nil || wallet.DepositAddress == nil {
		return nil, fmt.Errorf("sweep preflight: wallet %s not found or has no base address", walletID)
	}
	if wallet.Chain != chainID {
		return nil, fmt.Errorf("sweep preflight: wallet %s is on %s, plan is on %s", walletID, wallet.Chain, chainID)
	}
	adapter, err := s.registry.Chain(chainID)
	if err != nil {
		return nil, fmt.Errorf("sweep preflight: adapter for %q: %w", chainID, err)
	}
	shareA, err := s.decryptShareA(wallet, passphrase)
	if err != nil {
		return nil, err
	}
	shareB, err := s.fetchShareB(ctx, wallet)
	if err != nil {
		zeroBytes(shareA)
		return nil, err
	}
	return &preflightSession{
		wallet:  wallet,
		adapter: adapter,
		curve:   mpcpkg.Curve(wallet.MPCCurve),
		keys:    walletKeys{shareA: shareA, shareB: shareB, passphrase: passphrase},
	}, nil
}

func (p *preflightSession) close() {
	zeroBytes(p.keys.shareA)
	zeroBytes(p.keys.shareB)
}
