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
