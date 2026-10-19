package chain

import (
	"context"

	"github.com/macrowallets/waas/pkg/types"
)

// transactions.raw_payload keys of a broadcast withdrawal.
const (
	// BroadcastOutcomePayloadKey is BroadcastOutcomeUnknown on a withdrawal whose
	// broadcast answer did not say whether the network took it (ErrUnknownOutcome):
	// the row is tracked by the hash of its signed bytes like any sent transfer.
	BroadcastOutcomePayloadKey = "broadcast_outcome"
	BroadcastOutcomeUnknown    = "unknown"
	// SenderNoncePayloadKey is the nonce the signed transaction spends (EVM).
	SenderNoncePayloadKey = "sender_nonce"
	// BroadcastHeadPayloadKey is the chain head read just before the send (EVM): no
	// block before it can hold the transfer.
	BroadcastHeadPayloadKey = "broadcast_head"
)

// SenderNonceReader is an adapter whose signed transactions spend a sender nonce
// (EVM).
type SenderNonceReader interface {
	SenderNonce(signed *types.SignedTx) (uint64, error)
}

// NonceSpend is what the chain says about the transaction that spent a sender's
// nonce.
type NonceSpend struct {
	// Spent is false while no block at the chain's confirmation depth has spent it.
	Spent bool
	// Block is the block whose transaction spent it.
	Block uint64
	// Ours is true when that transaction is the one asked about.
	Ours bool
	// Result is our transaction's failure result when it was mined with a failed
	// receipt (out_of_gas, reverted); "" when it succeeded.
	Result string
}

// NonceSpendReader is an adapter that can say, by positive proof, which transaction
// spent a sender's nonce (EVM): the block where the sender's nonce count passed nonce,
// searched after fromBlock and at least the confirmation depth under the head, and
// the sender's transaction with that nonce in it. Ours there landed in that block;
// another one there means ours can never land. A read it cannot make (a node
// without the state of those blocks: no archive) is an error and proves nothing.
type NonceSpendReader interface {
	NonceSpend(ctx context.Context, txHash, from string, nonce, fromBlock uint64) (NonceSpend, error)
}
