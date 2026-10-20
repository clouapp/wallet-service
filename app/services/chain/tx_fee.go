package chain

import (
	"context"
	"errors"
	"math/big"
)

// TransactionFeeReader is an adapter that reads the fee a transaction it broadcast
// actually paid, in base units of the chain's native asset: what transactions.fee
// stores for withdrawals, sweeps and gas seeds once they confirm.
type TransactionFeeReader interface {
	TransactionFee(ctx context.Context, txHash string) (*big.Int, error)
}

// ErrTransactionFeeUnknown is a transaction the chain does not (yet) report.
var ErrTransactionFeeUnknown = errors.New("transaction fee not available")

// AccountRentPayloadKey is the transactions.raw_payload key of the native base units
// an outbound transfer moved, beside its fee, into accounts it created and its sender
// paid for (Solana: the rent of the recipient's associated token account), as a
// decimal string. A chain whose adapter is an AccountRentReader records it on each
// outbound transfer it confirms; transactions:backfill-fees fills the older ones.
const AccountRentPayloadKey = "account_rent"

// AccountRentReader is an adapter whose outbound transfers may pay, beside the fee,
// for accounts they create: TransactionAccountRent is what the transaction paid for
// them, in native base units, zero when it created none.
type AccountRentReader interface {
	TransactionAccountRent(ctx context.Context, txHash string) (*big.Int, error)
}
