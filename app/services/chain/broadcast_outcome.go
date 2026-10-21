package chain

import (
	"context"
	"errors"
	"math/big"

	"github.com/macrowallets/waas/pkg/types"
)

// transactions.raw_payload keys of a broadcast withdrawal.
const (
	// BroadcastOutcomePayloadKey is BroadcastOutcomeUnknown on a withdrawal whose
	// broadcast answer did not say whether the network took it (ErrUnknownOutcome):
	// the row is tracked by the hash of its signed bytes like any sent transfer.
	BroadcastOutcomePayloadKey = "broadcast_outcome"
	BroadcastOutcomeUnknown    = "unknown"
	// BroadcastOutcomePending is a row recorded before its send whose send was never
	// answered (a crash between the two, a refusal whose mark failed): a retry with
	// its key checks it (sweep.SettleUnanswered) instead of replaying it.
	BroadcastOutcomePending = "pending"
	// BroadcastOutcomeAccepted is a send a node took.
	BroadcastOutcomeAccepted = "accepted"
	// BroadcastOutcomeRefused is a send the network refused (the row is failed).
	BroadcastOutcomeRefused = "refused"
	// SignedTxPayloadKey is the signed bytes (hex) of a row whose send is pending: a
	// retry re-sends these same bytes, never a new signature. They are dropped once
	// the send is answered.
	SignedTxPayloadKey = "signed_tx"
	// SenderNoncePayloadKey is the nonce the signed transaction spends (EVM); a
	// scanned deposit keeps it too, next to SenderPayloadKey.
	SenderNoncePayloadKey = "sender_nonce"
	// SenderPayloadKey is the account that signed a scanned deposit's transaction
	// (EVM): with its nonce, it proves the transaction can never land.
	SenderPayloadKey = "sender"
	// BroadcastHeadPayloadKey is the chain head read just before the send (EVM): no
	// block before it can hold the transfer.
	BroadcastHeadPayloadKey = "broadcast_head"
	// ExpiryWitnessedAtPayloadKey is when (unix seconds) the tracker first saw one
	// endpoint alone not know a Solana transfer past its last valid block: a second
	// sight minutes later proves its expiry.
	ExpiryWitnessedAtPayloadKey = "expiry_witnessed_at"
	// NonceProofRetryAtPayloadKey is when (unix seconds) the tracker next tries the
	// nonce proof of a row the endpoints had no state to prove.
	NonceProofRetryAtPayloadKey = "nonce_proof_retry_at"
	// SpentInputsPayloadKey is the outpoints ("txid:vout") the signed transaction
	// spends (Bitcoin family).
	SpentInputsPayloadKey = "spent_inputs"
	// SpendProofRetryAtPayloadKey is when (unix seconds) the tracker next tries the
	// inputs proof of a Bitcoin-family row that nothing proved yet.
	SpendProofRetryAtPayloadKey = "spend_proof_retry_at"
	// LandedAsPayloadKey is the transaction that carried a Bitcoin-family transfer
	// when it is a copy of the signed one (a malleated or re-signed transaction
	// paying the same).
	LandedAsPayloadKey = "landed_as"
)

// SpentInputsReader is an adapter whose signed transactions spend outpoints
// (Bitcoin family): "txid:vout", in input order.
type SpentInputsReader interface {
	SpentInputs(signed *types.SignedTx) ([]string, error)
}

// InputsQuery is the transfer InputsSpend reads about: its hash, the outpoints it
// spends, and what it pays (To, Amount in base units). A transaction that spends
// them and pays the same is ours (a malleated or re-signed copy), unless OwnTransfer
// says it is another transfer of ours (another withdrawal paying the same): that
// one is a conflict. OwnTransfer may be nil; an error from it proves nothing.
type InputsQuery struct {
	TxHash      string
	Inputs      []string
	To          string
	Amount      *big.Int
	OwnTransfer func(txID string) (bool, error)
}

// InputsSpend is what a provider says about the inputs of a transfer the network
// does not know by its hash (Bitcoin family).
type InputsSpend struct {
	// Conflicted is true when every input is spent by another transaction, paying
	// someone else, at least the double-spend depth deep: ours can never land.
	Conflicted bool
	// Ours is true when an input is spent by the transfer itself or by a copy of it
	// (TxHash): it is in a block, or still in the mempool (Block 0).
	Ours bool
	// Block is our block when Ours, else the highest block of the spends that
	// conflict with it.
	Block uint64
	// TxHash is the transaction that carries ours when Ours: the transfer, or its copy.
	TxHash string
}

// InputsSpendReader is an adapter that can read which transaction spent each
// input of a transfer (Bitcoin family, from an Esplora provider): positive proof
// that it landed, or that other transactions spent every input it spends and it
// can never land. A provider that cannot say proves nothing.
type InputsSpendReader interface {
	InputsSpend(ctx context.Context, query InputsQuery) (InputsSpend, error)
}

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

// ErrHistoricalStateUnavailable is a proof that needs the state of old blocks no
// endpoint has (an archive endpoint is needed): the tracker backs the row off.
var ErrHistoricalStateUnavailable = errors.New("historical state unavailable")

// NonceSpendReader is an adapter that can say, by positive proof, which transaction
// spent a sender's nonce (EVM): the block where the sender's nonce count passed nonce,
// searched after fromBlock and at least the confirmation depth under the head, and
// the sender's transaction with that nonce in it. Ours there landed in that block;
// another one there means ours can never land. A read it cannot make (a node
// without the state of those blocks: no archive) is an error and proves nothing.
type NonceSpendReader interface {
	NonceSpend(ctx context.Context, txHash, from string, nonce, fromBlock uint64) (NonceSpend, error)
}

// TransactionSearcher is an adapter that can look a transaction up on every endpoint
// (EVM): GetTransactionBlock asked of each, the first mined receipt deciding. The
// operator's reconciliation uses it; the tracker keeps the failover order.
type TransactionSearcher interface {
	SearchTransactionBlock(ctx context.Context, txHash string) (uint64, error)
}

// ExpiryEvidence is what the endpoints say about a transfer whose last valid block
// may be past (Solana).
type ExpiryEvidence struct {
	// Past is true when the finalized block height is past the last valid block.
	Past bool
	// Known is true when an endpoint has the transaction: it is not expired.
	Known bool
	// Witnesses counts the endpoints that answered, for a slot at or past the
	// finalized one, that they do not know it.
	Witnesses int
}

// ExpiryEvidenceReader is an adapter whose expiry is proven by witnesses (Solana): a
// backend of a load-balanced endpoint behind the transaction's slot answers "not
// known" without an error, so one answer alone is not a proof (the tracker wants
// two witnesses, or one seen twice minutes apart).
type ExpiryEvidenceReader interface {
	ExpiryEvidence(ctx context.Context, txHash string, lastValidBlock uint64) (ExpiryEvidence, error)
}
