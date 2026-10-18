package chain

import "context"

// ReceiptStatusReader is an adapter whose blocks include transactions that failed:
// an EVM transaction mined with receipt status 0x0 ran out of gas or reverted, paid
// its fee and moved none of its value, while its block still lists it with that
// value. The deposit scanner asks it about every native transfer to a watched
// address before recording the deposit. The adapter's GetTransactionBlock reports
// such a transaction as a *TransactionFailedError, which the confirmation tracker
// reads before it confirms a native deposit.
type ReceiptStatusReader interface {
	// TransactionsSucceeded reads the receipts of txHashes, every one of them
	// included in block blockNum whose hash is blockHash, and reports for each one,
	// by its lowercase hash, whether its receipt succeeded. A transaction without a
	// receipt in that block, a receipt of another block or with an unknown status,
	// and a receipt that cannot be read are errors: no transaction is reported as
	// succeeded unless its receipt says so.
	TransactionsSucceeded(ctx context.Context, blockNum uint64, blockHash string, txHashes []string) (map[string]bool, error)
}
