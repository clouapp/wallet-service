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
	// SignatureAccounts returns the accounts whose transactions carry the deposits of
	// owner: owner itself, its associated token account of each registered token, and
	// every token account of a registered token that it owns, without repeats. A
	// registered token the node does not hold as a token mint is left out and named
	// in SkippedMints; a read that fails is an error.
	SignatureAccounts(ctx context.Context, owner string) (SignatureAccounts, error)
	// AccountSignatures lists, newest first, the finalized signatures of account that
	// query selects, failed transactions included and marked, every page from one
	// endpoint, with what that endpoint says of the end of the history.
	AccountSignatures(ctx context.Context, account string, query SignatureQuery) (SignatureHistory, error)
	// TransactionTransfers returns the credits of one finalized transaction, in the
	// order and at the positions the block scan reports them, with the transaction's
	// slot as BlockNumber and its block time; BlockHash is left empty.
	TransactionTransfers(ctx context.Context, signature string) (TransactionTransfers, error)
}

// SignatureAccounts is the accounts of one owner, and the registered mints left out
// because the node does not hold them as token mints (a mint of another network, a
// mistyped row): their deposits cannot be read until the registry is fixed.
type SignatureAccounts struct {
	Accounts     []string
	SkippedMints []string
}

// SignatureQuery selects part of the history of one account, read newest first, page
// by page: below Before (from the newest signature when empty) and above Until (both
// left out; empty for none). With HasFloor, the listing stops at the first signature
// below FloorSlot. MaxPages pages are read at most.
type SignatureQuery struct {
	Until     string
	Before    string
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

// SignatureHistory is what one SignatureQuery listed, newest first, from one endpoint.
// Head is that endpoint's finalized slot, read before the first page: a listing that
// ended holds every signature of the selected range up to it. More is a listing cut
// by MaxPages. Ended is a listing that reached the end of the range: Until (a short
// page the endpoint confirms: the signature right below what it listed is Until
// itself), the floor (FloorCrossed: a signature below it was seen), or, without
// either, an empty page, with LedgerStart the endpoint's first available block: only
// a ledger that starts at or below the floor proves there is nothing older to read. A
// listing with Until that is neither Ended nor More did not prove it reached Until.
type SignatureHistory struct {
	Signatures   []AccountSignature
	Head         uint64
	More         bool
	Ended        bool
	FloorCrossed bool
	LedgerStart  uint64
}

// TransactionTransfers is the credits of one finalized transaction and its slot.
type TransactionTransfers struct {
	Slot      uint64
	Transfers []types.DetectedTransfer
}
