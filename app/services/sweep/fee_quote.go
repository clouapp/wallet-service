package sweep

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// FeeQuoter prices a withdrawal with the planner and adapter code the withdrawal
// itself runs, without signing, broadcasting, locking, persisting or counting quota.
type FeeQuoter interface {
	QuoteWithdrawalFee(ctx context.Context, req FeeQuoteRequest) (*FeeQuote, error)
}

var _ FeeQuoter = (*service)(nil)

var (
	// ErrFeeQuoteNeedsTokenBalance: the wallet holds none of the EVM token, so the
	// node cannot simulate its transfer and no gas limit can be quoted.
	ErrFeeQuoteNeedsTokenBalance = errors.New("sweep: the wallet holds none of this token; its transfer cannot be simulated")
	// ErrFeeQuoteUnavailable wraps node failures (gas price, UTXOs, rent): the fee is unknown.
	ErrFeeQuoteUnavailable = errors.New("sweep: fee quote unavailable")
)

// FeeQuoteRequest asks what a withdrawal of Amount (base units) of Asset to
// ToAddress would pay in fees. An empty ToAddress prices a probe recipient.
type FeeQuoteRequest struct {
	WalletID        uuid.UUID
	Asset           string
	Amount          *big.Int
	ToAddress       string
	CallerAccountID uuid.UUID
}

// FeeBasis says which transfers a quote priced.
type FeeBasis string

const (
	// FeeBasisPlan prices every transfer of the plan the withdrawal would execute.
	FeeBasisPlan FeeBasis = "plan"
	// FeeBasisUnfundedDirect prices the transfer from the base address the withdrawal
	// would send once funded: the wallet cannot cover the amount today.
	FeeBasisUnfundedDirect FeeBasis = "direct_from_base_unfunded"
)

// FeeQuote is the native-coin fee of one withdrawal. Fee is never negative.
type FeeQuote struct {
	Chain            string
	Asset            string
	FeeAsset         string
	Amount           *big.Int
	Fee              *big.Int
	Strategy         Strategy
	Basis            FeeBasis
	AmountSpendable  bool
	Transfers        int
	RecipientIsProbe bool
	// FeeMultiplier is the wallet fee multiplier the priced transfers bid with (1 by default).
	FeeMultiplier    decimal.Decimal
	BaseBalance      *big.Int
	MinimumRemaining *big.Int
	EVM              *EVMFeeDetails
	Bitcoin          *BitcoinFeeDetails
	Solana           *SolanaFeeDetails
}

// EVMFeeDetails: every transfer is a legacy transaction paying GasPrice per gas.
type EVMFeeDetails struct {
	GasLimit  uint64
	GasPrice  *big.Int
	L1DataFee *big.Int
}

// BitcoinFeeDetails describes the rate and the size of the priced transactions;
// Inputs and Outputs are those of the withdrawal transaction itself.
type BitcoinFeeDetails struct {
	MilliSatPerVByte int64
	FlatFallback     bool
	VSize            int64
	Inputs           int
	Outputs          int
}

// SolanaFeeDetails sums signatures and token-account creations over the transfers.
type SolanaFeeDetails struct {
	Signatures              int
	LamportsPerSignature    int64
	AccountCreationLamports *big.Int
}

type bitcoinFeeQuoter interface {
	QuoteTransferFee(ctx context.Context, from string, amount *big.Int, pendingInputs []*big.Int) (chain.BitcoinFeeQuote, error)
	QuoteSweepFee(ctx context.Context, from string) (chain.BitcoinFeeQuote, error)
}

type solanaFeeQuoter interface {
	QuoteTransferFee(ctx context.Context, req types.TransferRequest) (chain.SolanaFeeQuote, error)
}

func (s *service) QuoteWithdrawalFee(ctx context.Context, req FeeQuoteRequest) (*FeeQuote, error) {
	if req.Amount == nil || req.Amount.Sign() <= 0 {
		return nil, fmt.Errorf("sweep: fee quote amount must be greater than zero")
	}
	wallet, err := s.walletRepo.FindByID(req.WalletID)
	if err != nil {
		return nil, fmt.Errorf("sweep: find wallet: %w", err)
	}
	if wallet == nil || wallet.DepositAddress == nil {
		return nil, fmt.Errorf("sweep: wallet %s not found or has no base address", req.WalletID)
	}
	chainEntity, err := s.chainRepo.FindByID(wallet.Chain)
	if err != nil || chainEntity == nil {
		return nil, fmt.Errorf("sweep: chain %q not found: %v", wallet.Chain, err)
	}
	adapter, err := s.registry.ChainForWallet(wallet)
	if err != nil {
		return nil, fmt.Errorf("sweep: adapter for %q: %w", wallet.Chain, err)
	}

	recipient := quoteRecipient(chainEntity.AdapterType, req.ToAddress)
	plan, err := s.PlanForWithdrawal(ctx, req.WalletID, req.Asset, req.Amount, recipient, req.CallerAccountID)
	if err != nil {
		return nil, err
	}

	quoted, basis := plan, FeeBasisPlan
	if plan.Strategy == StrategyInsufficient {
		quoted, basis = unfundedDirectPlan(plan, *wallet.DepositAddress), FeeBasisUnfundedDirect
	}
	quote := &FeeQuote{
		Chain:            plan.Chain,
		Asset:            plan.Asset,
		FeeAsset:         adapter.NativeAsset(),
		Amount:           copyBigInt(plan.Amount),
		Strategy:         plan.Strategy,
		Basis:            basis,
		AmountSpendable:  plan.ReachesTarget,
		RecipientIsProbe: req.ToAddress == "",
		FeeMultiplier:    chain.AppliedFeeMultiplier(adapter),
		BaseBalance:      copyBigInt(plan.BaseBalance),
		MinimumRemaining: new(big.Int),
	}
	target := gasPlanTarget{baseAddress: wallet.DepositAddress.Address, toAddress: recipient, includeFinalTransfer: true}

	switch chainEntity.AdapterType {
	case models.AdapterTypeEVM:
		err = s.quoteEVMFee(ctx, adapter, quoted, target, quote)
	case models.AdapterTypeBitcoin:
		err = quoteBitcoinFee(ctx, adapter, quoted, target, quote)
	case models.AdapterTypeSolana:
		err = s.quoteSolanaFee(ctx, adapter, quoted, target, quote)
	default:
		err = ErrUnsupportedChain
	}
	if err != nil {
		return nil, err
	}
	if quote.Fee == nil || quote.Fee.Sign() < 0 {
		return nil, fmt.Errorf("%w: chain %s priced the withdrawal at %v", ErrFeeQuoteUnavailable, plan.Chain, quote.Fee)
	}
	return quote, nil
}

// quoteRecipient is the recipient the planner and the adapters size transfers for:
// the caller's, or on EVM the fee probe (a token plan needs one to be simulated).
// Bitcoin fees do not depend on it and Solana substitutes its own probe.
func quoteRecipient(adapterType, toAddress string) string {
	if toAddress == "" && adapterType == models.AdapterTypeEVM {
		return chain.EVMFeeProbeRecipient()
	}
	return toAddress
}

// unfundedDirectPlan is the plan the withdrawal would run once the base address
// holds the amount: one transfer from base.
func unfundedDirectPlan(plan *Plan, base models.Address) *Plan {
	direct := *plan
	direct.Strategy = StrategyDirectFromBase
	direct.SourceAddress = &base
	direct.Sweeps = nil
	direct.Amount = copyBigInt(plan.Amount)
	return &direct
}

// quoteEVMFee prices the plan's transfers with the planner's own gas sizing
// (estimatedPlanGasUnits, planL1DataFees) at the price BuildTransfer encodes.
func (s *service) quoteEVMFee(ctx context.Context, adapter types.Chain, plan *Plan, target gasPlanTarget, quote *FeeQuote) error {
	estimator, ok := adapter.(types.TransferGasEstimator)
	if !ok {
		return fmt.Errorf("%w: chain %s cannot size its transfers", ErrUnsupportedChain, plan.Chain)
	}
	token, err := s.planToken(adapter, plan)
	if err != nil {
		return err
	}
	if token != nil && quote.Basis == FeeBasisUnfundedDirect {
		if err := capTokenAmountToBase(plan); err != nil {
			return err
		}
	}
	details, err := planGasBreakdown(ctx, adapter, estimator, plan, token, target)
	if err != nil {
		return err
	}
	fee := new(big.Int).Mul(details.GasPrice, new(big.Int).SetUint64(details.GasLimit))
	quote.Fee = fee.Add(fee, details.L1DataFee)
	quote.Transfers = len(planTransfers(plan, token, target))
	quote.EVM = details
	return nil
}

// capTokenAmountToBase lets the node simulate an unfunded token transfer: a transfer
// of more than the base holds reverts, while its gas does not depend on the amount.
func capTokenAmountToBase(plan *Plan) error {
	if plan.BaseBalance == nil || plan.BaseBalance.Sign() <= 0 {
		return ErrFeeQuoteNeedsTokenBalance
	}
	if plan.Amount.Cmp(plan.BaseBalance) > 0 {
		plan.Amount = new(big.Int).Set(plan.BaseBalance)
	}
	return nil
}

// planGasBreakdown is estimatePlanGas itemised: the summed gas limits, the gas
// price and the summed L1 data fees, failing instead of returning nil.
func planGasBreakdown(
	ctx context.Context,
	adapter types.Chain,
	estimator types.TransferGasEstimator,
	plan *Plan,
	token *types.Token,
	target gasPlanTarget,
) (*EVMFeeDetails, error) {
	gasUnits, known, err := estimatedPlanGasUnits(ctx, estimator, plan, token, target)
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, fmt.Errorf("sweep: fee quote: strategy %s on %s has no transfer to price", plan.Strategy, plan.Chain)
	}
	gasPrice, err := adapter.EstimateGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: gas price on %s: %v", ErrFeeQuoteUnavailable, plan.Chain, err)
	}
	if gasPrice == nil || gasPrice.Sign() <= 0 {
		return nil, fmt.Errorf("%w: %s returned no usable gas price", ErrFeeQuoteUnavailable, plan.Chain)
	}
	l1Fees, err := planL1DataFees(ctx, adapter, plan, token, target, gasPrice)
	if err != nil {
		return nil, err
	}
	return &EVMFeeDetails{GasLimit: gasUnits, GasPrice: new(big.Int).Set(gasPrice), L1DataFee: l1Fees}, nil
}

// quoteBitcoinFee prices each transfer with the coin selection BuildTransfer and
// BuildSweep run: sweep legs empty their source; the withdrawal from base may also
// spend the legs' outputs.
func quoteBitcoinFee(ctx context.Context, adapter types.Chain, plan *Plan, target gasPlanTarget, quote *FeeQuote) error {
	quoter, ok := adapter.(bitcoinFeeQuoter)
	if !ok {
		return fmt.Errorf("%w: chain %s cannot price its transfers", ErrUnsupportedChain, plan.Chain)
	}
	switch plan.Strategy {
	case StrategyDirectFromBase, StrategyDirectFromChild:
		if plan.SourceAddress == nil {
			return fmt.Errorf("sweep: fee quote: plan source address missing for strategy %s", plan.Strategy)
		}
		final, err := quoter.QuoteTransferFee(ctx, plan.SourceAddress.Address, plan.Amount, nil)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrFeeQuoteUnavailable, err)
		}
		setBitcoinQuote(quote, big.NewInt(final.Fee), final, final.VSize, 1)
		return nil
	case StrategyMultiSweep:
		fee, vsize := new(big.Int), int64(0)
		pending := make([]*big.Int, 0, len(plan.Sweeps))
		for _, leg := range plan.Sweeps {
			sweepQuote, err := quoter.QuoteSweepFee(ctx, leg.From.Address)
			if err != nil {
				return fmt.Errorf("%w: %v", ErrFeeQuoteUnavailable, err)
			}
			fee.Add(fee, big.NewInt(sweepQuote.Fee))
			vsize += sweepQuote.VSize
			pending = append(pending, leg.Amount)
		}
		final, err := quoter.QuoteTransferFee(ctx, target.baseAddress, plan.Amount, pending)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrFeeQuoteUnavailable, err)
		}
		setBitcoinQuote(quote, fee.Add(fee, big.NewInt(final.Fee)), final, vsize+final.VSize, len(plan.Sweeps)+1)
		return nil
	default:
		return fmt.Errorf("sweep: fee quote: strategy %s has no transfer to price", plan.Strategy)
	}
}

func setBitcoinQuote(quote *FeeQuote, fee *big.Int, final chain.BitcoinFeeQuote, vsize int64, transfers int) {
	quote.Fee = fee
	quote.Transfers = transfers
	quote.Bitcoin = &BitcoinFeeDetails{
		MilliSatPerVByte: final.MilliSatPerVByte,
		FlatFallback:     final.FlatFallback,
		VSize:            vsize,
		Inputs:           final.Inputs,
		Outputs:          final.Outputs,
	}
}

// quoteSolanaFee prices every transfer the executor sends for the plan with the
// adapter's transfer pricing; a native withdrawal also reports the rent-exempt
// minimum the planner keeps on the source.
func (s *service) quoteSolanaFee(ctx context.Context, adapter types.Chain, plan *Plan, target gasPlanTarget, quote *FeeQuote) error {
	quoter, ok := adapter.(solanaFeeQuoter)
	if !ok {
		return fmt.Errorf("%w: chain %s cannot price its transfers", ErrUnsupportedChain, plan.Chain)
	}
	token, err := s.planToken(adapter, plan)
	if err != nil {
		return err
	}
	transfers := planTransfers(plan, token, target)
	if len(transfers) == 0 {
		return fmt.Errorf("sweep: fee quote: strategy %s has no transfer to price", plan.Strategy)
	}
	fee := new(big.Int)
	details := &SolanaFeeDetails{AccountCreationLamports: new(big.Int)}
	for _, transfer := range transfers {
		transferQuote, err := quoter.QuoteTransferFee(ctx, transfer)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrFeeQuoteUnavailable, err)
		}
		fee.Add(fee, transferQuote.Fee())
		details.Signatures += transferQuote.Signatures
		details.LamportsPerSignature = transferQuote.LamportsPerSignature
		details.AccountCreationLamports.Add(details.AccountCreationLamports, transferQuote.AccountCreationLamports)
	}
	if token == nil {
		reserve, err := loadNativeReserve(ctx, adapter, plan.Asset)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrFeeQuoteUnavailable, err)
		}
		quote.MinimumRemaining = reserve.minimumRemaining
	}
	quote.Fee = fee
	quote.Transfers = len(transfers)
	quote.Solana = details
	return nil
}
