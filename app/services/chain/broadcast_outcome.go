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
	// SenderNoncePayloadKey is the nonce the signed transaction spends (EVM), so the
	// tracker can tell a transfer that can never land: its nonce was mined by another
	// transaction and no node knows its hash.
	SenderNoncePayloadKey = "sender_nonce"
)

// SenderNonceReader is an adapter whose signed transactions spend a sender nonce
// (EVM).
type SenderNonceReader interface {
	SenderNonce(signed *types.SignedTx) (uint64, error)
}

// TransactionSupersededChecker is an adapter that can prove a broadcast transaction
// will never be included: no node knows txHash and the sender's mined nonce is
// already past nonce (another transaction took it). Only the EVM adapter
// implements it.
type TransactionSupersededChecker interface {
	TransactionSuperseded(ctx context.Context, txHash, from string, nonce uint64) (bool, error)
}
