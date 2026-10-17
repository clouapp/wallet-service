package chain

import (
	"context"
	"errors"
	"fmt"
	"math/big"
)

// XRPPaymentFunds is what one native XRP Ledger payment from Source can spend, read
// from the validated ledger. All amounts are drops. Spendable is
// Balance − Reserve − Fee; it may be negative. The destination fields are set only
// when the caller named a destination.
// The live XRP client returns this shape; sweep keeps the port.
type XRPPaymentFunds struct {
	Balance *big.Int
	// Reserve is the source's account reserve: base reserve + owner count × increment.
	Reserve *big.Int
	// Fee is the open-ledger fee the payment would bid.
	Fee *big.Int
	// BaseReserve is the least a payment must deliver to create a new account.
	BaseReserve            *big.Int
	DestinationChecked     bool
	DestinationFunded      bool
	DestinationRequiresTag bool
}

// Spendable is the most a payment can deliver: Balance − Reserve − Fee. A nil
// amount counts as zero.
func (f XRPPaymentFunds) Spendable() *big.Int {
	spendable := new(big.Int).Sub(orZero(f.Balance), orZero(f.Reserve))
	return spendable.Sub(spendable, orZero(f.Fee))
}

func orZero(value *big.Int) *big.Int {
	if value == nil {
		return new(big.Int)
	}
	return value
}

// CheckDestination refuses a payment of amount drops that the destination would
// reject on the ledger. tagged says the payment carries a destination tag. It
// passes when no destination was checked.
func (f XRPPaymentFunds) CheckDestination(amount *big.Int, tagged bool) error {
	if !f.DestinationChecked {
		return nil
	}
	if f.DestinationRequiresTag && !tagged {
		return ErrDestinationTagRequired
	}
	if !f.DestinationFunded && (amount == nil || f.BaseReserve == nil || amount.Cmp(f.BaseReserve) < 0) {
		return fmt.Errorf("%w: %s drops is less than the %s-drop base reserve", ErrDestinationBelowReserve, amount, f.BaseReserve)
	}
	return nil
}

var (
	// ErrDestinationBelowReserve is a payment to an account that does not exist yet
	// of less than the base reserve: the ledger would refuse it (tecNO_DST_INSUF_XRP).
	ErrDestinationBelowReserve = errors.New("destination account is not funded and the amount is below the base reserve")
	// ErrDestinationTagRequired is a payment to an account that requires a destination
	// tag (lsfRequireDestTag), which this payment does not carry.
	ErrDestinationTagRequired = errors.New("destination account requires a destination tag")
	// ErrTransactionFailed is a transaction a validated ledger or a block includes with
	// a failure result (an XRP Ledger tec code, an EVM receipt with status 0x0): its
	// fee was paid, its payment was not made.
	ErrTransactionFailed = errors.New("transaction failed on chain")
)

// TransactionFailedError is ErrTransactionFailed with the chain's result code and the
// ledger (block) that includes the transaction.
type TransactionFailedError struct {
	Result string
	Block  uint64
}

func (e *TransactionFailedError) Error() string {
	return fmt.Sprintf("%s: %s in block %d", ErrTransactionFailed, e.Result, e.Block)
}

func (e *TransactionFailedError) Is(target error) bool {
	return target == ErrTransactionFailed
}

// LastValidBlockPayloadKey is the transactions.raw_payload key holding the last
// block a broadcast transaction can be included in (XRP LastLedgerSequence).
const LastValidBlockPayloadKey = "last_ledger_sequence"

// TransactionExpiryChecker is an adapter that can prove a broadcast transaction will
// never be included: the chain is past lastValidBlock and the node searched every
// block the transaction could be in. Only the XRP adapter implements it.
type TransactionExpiryChecker interface {
	TransactionExpired(ctx context.Context, txHash string, lastValidBlock uint64) (bool, error)
}
