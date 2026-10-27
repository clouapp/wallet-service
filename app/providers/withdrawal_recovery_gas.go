package providers

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/sweep"
	recovery "github.com/macrowallets/waas/app/services/withdrawrecovery"
	runtime "github.com/macrowallets/waas/app/services/withdrawrecoveryruntime"
)

type recoveryGasObserver interface {
	Observe(context.Context, recovery.Binding, string) (*recovery.Observation, error)
}
type recoveryGasBridge struct {
	preparer  sweep.DirectChildGasPreparer
	observer  recoveryGasObserver
	endpoints [2]runtime.Endpoint
	guard     func(context.Context) error
	now       func() time.Time
}

func directRecoveryGasBinding(b recovery.Binding, claim uuid.UUID) (sweep.DirectChildGasBinding, error) {
	if b.TokenDecimals < 0 || b.TokenDecimals > 36 {
		return sweep.DirectChildGasBinding{}, recovery.ErrRefused
	}
	return sweep.DirectChildGasBinding{OperationID: b.OperationID, ClaimID: claim, OriginalTransactionID: b.OriginalTransactionID, AccountID: b.AccountID, WalletID: b.WalletID, SourceAddressID: b.SourceAddressID, SourceAddress: b.SourceAddress, SourceDerivationIndex: b.SourceDerivationIndex, SourceExternalUserID: b.SourceExternalUserID, SourceNonce: b.Nonce, Chain: b.Chain, NetworkID: b.NetworkID, Asset: b.Asset, TokenContract: b.TokenContract, TokenDecimals: uint8(b.TokenDecimals), AmountRaw: b.AmountRaw, Recipient: b.Recipient}, nil
}
func recoveryInteger(s string) (*big.Int, error) {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.Sign() < 0 || n.BitLen() > 256 {
		return nil, recovery.ErrRefused
	}
	return n, nil
}
func recoveryFee(q recovery.FeeQuote) (*big.Int, error) {
	price, err := recoveryInteger(q.GasPriceWei)
	if err != nil {
		return nil, err
	}
	l1, err := recoveryInteger(q.L1FeeWei)
	if err != nil {
		return nil, err
	}
	if q.GasLimit == 0 || price.Sign() == 0 {
		return nil, recovery.ErrRefused
	}
	return new(big.Int).Add(new(big.Int).Mul(new(big.Int).SetUint64(q.GasLimit), price), l1), nil
}
func (g *recoveryGasBridge) quote(ctx context.Context, b recovery.Binding) (*recovery.GasSeedPlan, error) {
	observation, err := g.observer.Observe(ctx, b, "")
	if err != nil {
		return nil, err
	}
	if observation == nil || !observation.RPCsAgree || observation.OriginalKnown || observation.CandidateKnown || observation.ReceiptObserved {
		return nil, recovery.ErrNeedsReconciliation
	}
	fee, err := recoveryFee(observation.Quote)
	if err != nil {
		return nil, err
	}
	balance, err := recoveryInteger(observation.SourceNativeBalanceWei)
	if err != nil {
		return nil, err
	}
	if balance.Cmp(fee) >= 0 {
		return nil, nil
	}
	// No claim exists during inspection. This operation-derived identifier is
	// used only by the read-only quote; actual preparation always uses Attempt.ID.
	binding, err := directRecoveryGasBinding(b, b.OperationID)
	if err != nil {
		return nil, err
	}
	quote, err := g.preparer.QuoteDirectChildGas(ctx, binding)
	if err != nil {
		return nil, err
	}
	if quote == nil {
		return nil, recovery.ErrRefused
	}
	nonce, err := g.baseNonce(ctx, b.NetworkID, quote.BaseAddress)
	if err != nil {
		return nil, err
	}
	return &recovery.GasSeedPlan{BaseAddressID: quote.BaseAddressID, BaseAddress: quote.BaseAddress, Nonce: &nonce, AmountWei: quote.AmountWei, MaxFeeWei: quote.MaxFeeWei}, nil
}
func (g *recoveryGasBridge) baseNonce(ctx context.Context, network int64, address string) (uint64, error) {
	if network <= 0 || address == "" {
		return 0, recovery.ErrRefused
	}
	var expected *uint64
	for _, endpoint := range g.endpoints {
		if endpoint.Client == nil {
			return 0, recovery.ErrRefused
		}
		var encoded string
		if endpoint.Client.Call(ctx, "eth_chainId", &encoded) != nil {
			return 0, recovery.ErrRefused
		}
		id, ok := new(big.Int).SetString(strings.TrimPrefix(encoded, "0x"), 16)
		if !ok || id.Cmp(big.NewInt(network)) != 0 {
			return 0, recovery.ErrRefused
		}
		for _, tag := range []string{"latest", "pending"} {
			if endpoint.Client.Call(ctx, "eth_getTransactionCount", &encoded, address, tag) != nil {
				return 0, recovery.ErrRefused
			}
			nonce, ok := new(big.Int).SetString(strings.TrimPrefix(encoded, "0x"), 16)
			if !ok || !nonce.IsUint64() {
				return 0, recovery.ErrNeedsReconciliation
			}
			value := nonce.Uint64()
			if expected == nil {
				expected = &value
			} else if *expected != value {
				return 0, recovery.ErrNeedsReconciliation
			}
		}
	}
	if expected == nil {
		return 0, recovery.ErrRefused
	}
	return *expected, nil
}
func recoveryGasProposal(row *models.WithdrawalRecoveryAttempt, b recovery.Binding, p recovery.GasSeedPlan, now time.Time) (recovery.Proposal, time.Time, error) {
	var proposal recovery.Proposal
	if row == nil || row.ID == uuid.Nil || row.OperationID != b.OperationID || row.OriginalTransactionID != b.OriginalTransactionID || row.AccountID != b.AccountID || row.WalletID != b.WalletID || row.SourceAddressID != b.SourceAddressID || row.State != models.RecoveryGasClaimed {
		return proposal, time.Time{}, recovery.ErrRefused
	}
	if json.Unmarshal([]byte(row.ProposalJSON), &proposal) != nil || proposal.GasSeed == nil {
		return proposal, time.Time{}, recovery.ErrRefused
	}
	bindingHash, _ := recovery.Hash(b)
	reviewedBindingHash, _ := recovery.Hash(proposal.Binding)
	planHash, _ := recovery.Hash(p)
	reviewedPlanHash, _ := recovery.Hash(proposal.GasSeed)
	proposalHash, _ := recovery.Hash(proposal)
	if bindingHash != row.BindingHash || bindingHash != reviewedBindingHash || planHash != reviewedPlanHash || proposalHash != row.ProposalHash || !proposal.ExpiresAt.After(now) {
		return proposal, time.Time{}, recovery.ErrRefused
	}
	expiry, reviewErr := runtime.ApprovedReviewExpiry(row, proposal, now)
	if reviewErr != nil {
		return proposal, time.Time{}, reviewErr
	}
	return proposal, expiry, nil
}
func (g *recoveryGasBridge) frozenPlan(ctx context.Context, row *models.WithdrawalRecoveryAttempt, b recovery.Binding, p recovery.GasSeedPlan) (sweep.DirectChildGasPlan, error) {
	var result sweep.DirectChildGasPlan
	proposal, reviewExpiry, err := recoveryGasProposal(row, b, p, g.currentTime())
	if err != nil {
		return result, err
	}
	if p.Nonce == nil {
		return result, recovery.ErrRefused
	}
	nonce, err := g.baseNonce(ctx, b.NetworkID, p.BaseAddress)
	if err != nil {
		return result, err
	}
	if nonce != *p.Nonce {
		return result, recovery.ErrNeedsReconciliation
	}
	binding, bindErr := directRecoveryGasBinding(b, row.ID)
	if bindErr != nil {
		return result, bindErr
	}
	current, err := g.preparer.QuoteDirectChildGas(ctx, binding)
	if err != nil {
		return result, err
	}
	if current == nil || current.BaseAddressID != p.BaseAddressID || !strings.EqualFold(current.BaseAddress, p.BaseAddress) {
		return result, recovery.ErrRefused
	}
	for _, pair := range [][2]string{{current.AmountWei, p.AmountWei}, {current.MaxFeeWei, p.MaxFeeWei}, {current.TokenFeeWei, proposal.Fees.MaxNativeFeeWei}} {
		actual, e := recoveryInteger(pair[0])
		if e != nil {
			return result, e
		}
		cap, e := recoveryInteger(pair[1])
		if e != nil || actual.Cmp(cap) > 0 {
			return result, recovery.ErrRefused
		}
	}
	// Only safety diagnostics are refreshed. Every financial field remains the
	// exact reviewed base, nonce, seed amount and fee cap, with original expiry.
	return sweep.DirectChildGasPlan{BaseAddressID: p.BaseAddressID, BaseAddress: p.BaseAddress, FunderNonce: p.Nonce, AmountWei: p.AmountWei, MaxFeeWei: p.MaxFeeWei, MinFunderBalanceWei: current.MinFunderBalanceWei, TokenFeeWei: current.TokenFeeWei, ExpiresAt: reviewExpiry}, nil
}
func (g *recoveryGasBridge) Prepare(ctx context.Context, row *models.WithdrawalRecoveryAttempt, b recovery.Binding, p recovery.GasSeedPlan, passphrase string) (*recovery.GasResult, error) {
	plan, err := g.frozenPlan(ctx, row, b, p)
	if err != nil {
		return nil, err
	}
	ctx = withRecoveryReviewDeadline(ctx, plan.ExpiresAt)
	if deadlineErr := gateRecoveryReviewDeadline(ctx, g.currentTime()); deadlineErr != nil {
		return nil, deadlineErr
	}
	if g.guard == nil {
		return nil, recovery.ErrRefused
	}
	if gateErr := g.guard(ctx); gateErr != nil {
		return nil, gateErr
	}
	binding, bindErr := directRecoveryGasBinding(b, row.ID)
	if bindErr != nil {
		return nil, bindErr
	}
	result, err := g.preparer.PrepareDirectChildGas(ctx, binding, plan, passphrase)
	return mapRecoveryGasResult(result), err
}
func (g *recoveryGasBridge) Observe(ctx context.Context, row *models.WithdrawalRecoveryAttempt) (*recovery.GasResult, error) {
	if row == nil {
		return nil, recovery.ErrRefused
	}
	var b recovery.Binding
	if json.Unmarshal([]byte(row.BindingJSON), &b) != nil {
		return nil, recovery.ErrRefused
	}
	hash, _ := recovery.Hash(b)
	if hash != row.BindingHash {
		return nil, recovery.ErrRefused
	}
	binding, bindErr := directRecoveryGasBinding(b, row.ID)
	if bindErr != nil {
		return nil, bindErr
	}
	result, err := g.preparer.ObserveDirectChildGas(ctx, binding)
	return mapRecoveryGasResult(result), err
}
func (g *recoveryGasBridge) Resume(ctx context.Context, row *models.WithdrawalRecoveryAttempt, b recovery.Binding, p recovery.GasSeedPlan, passphrase string) (*recovery.GasResult, error) {
	_, reviewExpiry, reviewErr := recoveryGasProposal(row, b, p, g.currentTime())
	if reviewErr != nil {
		return nil, reviewErr
	}
	ctx = withRecoveryReviewDeadline(ctx, reviewExpiry)
	binding, bindErr := directRecoveryGasBinding(b, row.ID)
	if bindErr != nil {
		return nil, bindErr
	}
	observed, err := g.preparer.ObserveDirectChildGas(ctx, binding)
	if errors.Is(err, models.ErrRepositoryNotFound) {
		return g.Prepare(ctx, row, b, p, passphrase)
	}
	if err != nil {
		return mapRecoveryGasResult(observed), err
	}
	// An included seed is never sent again, even if the child's gas was spent.
	if observed == nil {
		return nil, recovery.ErrNeedsReconciliation
	}
	if observed.Included || observed.Confirmed {
		return mapRecoveryGasResult(observed), nil
	}
	if deadlineErr := gateRecoveryReviewDeadline(ctx, g.currentTime()); deadlineErr != nil {
		return nil, deadlineErr
	}
	if g.guard == nil {
		return nil, recovery.ErrRefused
	}
	if gateErr := g.guard(ctx); gateErr != nil {
		return nil, gateErr
	}
	result, err := g.preparer.ResumeDirectChildGas(ctx, binding)
	return mapRecoveryGasResult(result), err
}
func mapRecoveryGasResult(r *sweep.DirectChildGasResult) *recovery.GasResult {
	if r == nil {
		return nil
	}
	// FeeWei is the immutable signed fee bound, never a claim of fees paid.
	// Confirmed means successful inclusion plus current gas readiness, not finality.
	return &recovery.GasResult{TransactionID: r.TransactionID, TxHash: r.TxHash, NetworkID: r.NetworkID, From: r.From, To: r.To, Nonce: r.Nonce, AmountWei: r.AmountWei, FeeWei: r.FeeWei, Confirmed: r.Confirmed}
}

var _ recovery.GasPreparer = (*recoveryGasBridge)(nil)
var _ recovery.GasPreparationResumer = (*recoveryGasBridge)(nil)

func (g *recoveryGasBridge) currentTime() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now().UTC()
}
