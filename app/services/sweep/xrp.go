package sweep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

var (
	// ErrDestinationBelowReserve is chain.ErrDestinationBelowReserve, re-exported for controllers.
	ErrDestinationBelowReserve = chain.ErrDestinationBelowReserve
	// ErrDestinationTagRequired is chain.ErrDestinationTagRequired, re-exported for controllers.
	ErrDestinationTagRequired = chain.ErrDestinationTagRequired
	// ErrDestinationTagUnsupported is a destination tag on a chain without tags (only XRP has them).
	ErrDestinationTagUnsupported = errors.New("sweep: destination tags are only supported on XRP")
)

// Destination is where a withdrawal sends. Tag is the XRP Ledger destination tag;
// nil sends none.
type Destination struct {
	Address string
	Tag     *uint32
}

// destinationTagger is implemented by adapters whose transfers carry
// TransferRequest.DestinationTag (XRP).
type destinationTagger interface {
	SupportsDestinationTag() bool
}

func supportsDestinationTag(adapter types.Chain) bool {
	tagger, ok := adapter.(destinationTagger)
	return ok && tagger.SupportsDestinationTag()
}

// lastValidBlocker is implemented by adapters whose signed transactions expire
// after a block (XRP LastLedgerSequence); the confirmation tracker reads it back
// from the transaction's raw_payload to tell an expired payment from a slow one.
type lastValidBlocker interface {
	LastValidBlock(signed *types.SignedTx) (uint64, error)
}

// broadcastRawPayload is the raw_payload of a broadcast transaction: "{}", or the
// last valid block of a transaction that expires.
func broadcastRawPayload(adapter types.Chain, signed *types.SignedTx) (string, error) {
	blocker, ok := adapter.(lastValidBlocker)
	if !ok {
		return "{}", nil
	}
	last, err := blocker.LastValidBlock(signed)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(map[string]uint64{chain.LastValidBlockPayloadKey: last})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// xrpPaymentFundsReader is the XRP Ledger adapter's read of what one payment from an
// account can spend. An empty `to` skips the destination.
type xrpPaymentFundsReader interface {
	PaymentFunds(ctx context.Context, from, to string) (chain.XRPPaymentFunds, error)
}

// BaseReserveShortfallError is an XRP withdrawal the wallet's base address cannot
// pay: amount + fee + the account reserve is more than it holds. XRP withdrawals
// never sweep child addresses. It is ErrInsufficientFunds.
type BaseReserveShortfallError struct {
	Address string
	Amount  *big.Int
	Funds   chain.XRPPaymentFunds
}

func (e *BaseReserveShortfallError) Error() string {
	spendable := e.Funds.Spendable()
	if spendable.Sign() < 0 {
		spendable.SetInt64(0)
	}
	return fmt.Sprintf("sweep: XRP withdrawals spend only the base address %s: it holds %s drops and must keep a %s-drop account reserve, so it cannot send %s drops plus the %s-drop fee (at most %s drops); child addresses are not swept",
		e.Address, dropsText(e.Funds.Balance), dropsText(e.Funds.Reserve), dropsText(e.Amount), dropsText(e.Funds.Fee), spendable)
}

func dropsText(value *big.Int) string {
	if value == nil {
		return "0"
	}
	return value.String()
}

func (e *BaseReserveShortfallError) Is(target error) bool {
	return target == ErrInsufficientFunds
}

// planXRPWithdrawal plans an XRP withdrawal from the wallet's base address only.
// When the base address cannot pay amount + fee + its account reserve the plan is
// StrategyInsufficient with a BaseReserveShortfallError; children are never swept.
// A destination the ledger would refuse the payment to (an account that does not
// exist receiving less than the base reserve, or one that requires a destination
// tag when the withdrawal carries none) fails the plan.
func planXRPWithdrawal(ctx context.Context, adapter types.Chain, wallet *models.Wallet, asset string, amount *big.Int, to Destination) (*Plan, error) {
	toAddress := to.Address
	if !types.SameAssetSymbol(asset, adapter.NativeAsset()) {
		return nil, fmt.Errorf("%w: XRP withdrawals send native %s only", ErrUnsupportedChain, adapter.NativeAsset())
	}
	if reporter, ok := adapter.(testnetReporter); !ok || !reporter.IsTestnet() {
		return nil, fmt.Errorf("%w: XRP payments are enabled on the altnet only", ErrUnsupportedChain)
	}
	reader, ok := adapter.(xrpPaymentFundsReader)
	if !ok {
		return nil, fmt.Errorf("%w: chain %s cannot read payment funds", ErrUnsupportedChain, wallet.Chain)
	}
	base := *wallet.DepositAddress
	funds, err := reader.PaymentFunds(ctx, base.Address, toAddress)
	if err != nil {
		return nil, fmt.Errorf("sweep: xrp payment funds: %w", err)
	}
	if err := funds.CheckDestination(amount, to.Tag != nil); err != nil {
		return nil, err
	}
	plan := &Plan{
		WalletID:       wallet.ID,
		Chain:          wallet.Chain,
		Asset:          asset,
		Amount:         new(big.Int).Set(amount),
		BaseBalance:    new(big.Int).Set(funds.Balance),
		EstimatedGas:   new(big.Int).Set(funds.Fee),
		DestinationTag: copyTag(to.Tag),
	}
	if funds.Spendable().Cmp(amount) < 0 {
		plan.Strategy = StrategyInsufficient
		plan.Shortfall = &BaseReserveShortfallError{Address: base.Address, Amount: new(big.Int).Set(amount), Funds: funds}
		return plan, nil
	}
	plan.Strategy = StrategyDirectFromBase
	plan.SourceAddress = &base
	plan.ReachesTarget = true
	return plan, nil
}

// quoteXRPFee prices the one payment from the base address: the open-ledger fee
// BuildTransfer bids. MinimumRemaining is the base address's account reserve.
func quoteXRPFee(ctx context.Context, adapter types.Chain, plan *Plan, target gasPlanTarget, quote *FeeQuote) error {
	reader, ok := adapter.(xrpPaymentFundsReader)
	if !ok {
		return fmt.Errorf("%w: chain %s cannot price its transfers", ErrUnsupportedChain, plan.Chain)
	}
	if plan.Strategy != StrategyDirectFromBase {
		return fmt.Errorf("sweep: fee quote: XRP strategy %s has no transfer to price", plan.Strategy)
	}
	funds, err := reader.PaymentFunds(ctx, target.baseAddress, target.toAddress)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrFeeQuoteUnavailable, err)
	}
	quote.Fee = new(big.Int).Set(funds.Fee)
	quote.Transfers = 1
	quote.MinimumRemaining = new(big.Int).Set(funds.Reserve)
	quote.XRP = &XRPFeeDetails{
		FeeDrops:       new(big.Int).Set(funds.Fee),
		AccountReserve: new(big.Int).Set(funds.Reserve),
		BaseReserve:    new(big.Int).Set(funds.BaseReserve),
	}
	return nil
}

func copyTag(tag *uint32) *uint32 {
	if tag == nil {
		return nil
	}
	copied := *tag
	return &copied
}
