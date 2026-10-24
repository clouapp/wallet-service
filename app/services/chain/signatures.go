package chain

import (
	"context"

	"github.com/macrowallets/waas/pkg/types"
)

// SignatureReader is an adapter whose deposits can be read account by account, from
// the signatures of each account's transactions, instead of block by block (Solana:
// getSignaturesForAddress, then getTransaction). The deposit service's signature sync
// reads a chain this way when reading every block costs too much; the credits of a
// transaction are the ones its block scan finds, at the same positions, so the two
// paths (and the provider push) record one deposit per transfer. BlockHashAt fills
// the block hash of a slot that credits a watched address.
type SignatureReader interface {
	BlockHashReader
	// SignatureAccounts returns, without repeats, the accounts whose transactions
	// carry the deposits of owner: owner itself, its associated token account of
	// each registered token, and every token account of a registered token that it
	// owns.
	SignatureAccounts(ctx context.Context, owner string) ([]string, error)
	// AccountSignatures returns the finalized signatures of account that query
	// selects, oldest first, failed transactions included and marked.
	AccountSignatures(ctx context.Context, account string, query SignatureQuery) (SignatureHistory, error)
	// TransactionTransfers returns the credits of one finalized transaction, in the
	// order and at the positions the block scan reports them, with the transaction's
	// slot as BlockNumber and its block time; BlockHash is left empty.
	TransactionTransfers(ctx context.Context, signature string) (TransactionTransfers, error)
}

// SignatureQuery selects the signatures of one account, newest first on the node,
// page by page. Until is the newest signature already processed (it and the older
// ones are left out; empty for none). With HasFloor, the history stops before the
// first signature below FloorSlot. MaxPages bounds how many pages are read: a history
// that goes on past it is returned cut (SignatureHistory.Truncated).
type SignatureQuery struct {
	Until     string
	FloorSlot uint64
	HasFloor  bool
	MaxPages  int
}

// AccountSignature is one signature of an account's history: its slot, and whether
// its transaction failed on chain (it moved no value and credits nothing).
type AccountSignature struct {
	Signature string
	Slot      uint64
	Failed    bool
}

// SignatureHistory is the signatures a SignatureQuery selected, oldest first.
// Truncated means MaxPages was reached before the history ended (or reached the
// floor): older signatures were not read.
type SignatureHistory struct {
	Signatures []AccountSignature
	Truncated  bool
}

// TransactionTransfers is the credits of one finalized transaction and its slot.
type TransactionTransfers struct {
	Slot      uint64
	Transfers []types.DetectedTransfer
}
