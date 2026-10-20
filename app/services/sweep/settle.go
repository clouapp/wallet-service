package sweep

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// ErrSendUnverified is a withdrawal row whose send was never answered and whose
// same bytes reached no node now: it is neither replayed as broadcast nor signed
// again. A later retry checks it again; withdrawals:reconcile settles it.
var ErrSendUnverified = errors.New("the withdrawal's send could not be verified yet")

// ErrSignedBytesInvalid is a withdrawal row whose signed bytes a node refused for a
// reason no time can change (a signature or sender that does not verify, another
// chain id, bytes that do not decode, gas below the intrinsic cost, data too
// large): they were never valid, so they were never sent. The row is failed.
var ErrSignedBytesInvalid = errors.New("the withdrawal's signed bytes can never be valid")

// Refusals that prove signed bytes can never be valid, whenever they are sent
// (geth, Nethermind and Erigon EVM answers, Solana, TRON, the Bitcoin family, XRP).
// Every other refusal (nonce too low, underpriced, a full pool, a fee cap under the
// base fee, insufficient funds) can follow the original's own landing or depends on
// the moment, and proves nothing.
var neverValidPhrases = []string{
	"invalid sender", "invalid signature", "invalid chain id", "chain id mismatch",
	"intrinsic gas too low", "oversized data", "transaction type not supported", "rlp:",
	"signature verification failure", "signature error", "mandatory-script-verify-flag-failed", "tembad_signature",
}

// Answers of a node that already holds these exact bytes, for an adapter that
// returns them as a refusal. A TRON DUP_TRANSACTION_ERROR is not one: the node's
// recent-id cache also remembers a push it refused.
var alreadyHeldPhrases = []string{
	"already known", "known transaction", "already imported", "already in mempool", "already been processed",
	"txn-already-in-mempool", "txn-already-known", "transaction already in block chain", "tefalready",
}

// SettleUnanswered settles a withdrawal row recorded before its send whose send was
// never answered (broadcast_outcome pending: a crash between the insert and the
// answer, or a refusal whose mark failed), before a retry with its key replays it.
// It never signs: a second signature could pay the withdrawal twice.
//
//  1. its hash on every endpoint: found (mined, or failed on chain) is sent;
//  2. the same signed bytes, kept sealed in raw_payload, sent again: accepted, or
//     already held by the node, is sent; an unknown answer is unknown; reaching no
//     node is ErrSendUnverified and changes nothing;
//  3. any refusal is an unknown outcome: the original may be in another pool or
//     mined already, and the tracker's positive proofs (EVM nonce spend, XRP and
//     Solana expiry) settle it. Only a refusal proving the bytes can never be valid
//     (neverValidPhrases) fails the row, as ErrSignedBytesInvalid.
//
// A row whose bytes were not kept, or do not open, is an unknown outcome too. The
// row returned (sent or unknown) is replayed as broadcast; the bytes are dropped
// from it.
func (s *service) SettleUnanswered(ctx context.Context, tx *models.Transaction) (*models.Transaction, error) {
	if tx == nil {
		return nil, fmt.Errorf("settle unanswered withdrawal: transaction is required")
	}
	adapter, err := s.registry.Chain(tx.Chain)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSendUnverified, err)
	}
	block, err := searchBlock(ctx, adapter, tx.TxHash)
	var onChain *chain.TransactionFailedError
	switch {
	case errors.As(err, &onChain) || (err == nil && block > 0):
		return s.recordAnswer(ctx, tx, chain.BroadcastOutcomeAccepted), nil
	case err != nil:
		return nil, fmt.Errorf("%w: search %s: %w", ErrSendUnverified, tx.TxHash, err)
	}
	raw, ok := s.keptSignedBytes(tx)
	if !ok {
		slog.Warn("withdrawal send never answered and its signed bytes cannot be sent again; recorded as an unknown outcome",
			"tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain)
		return s.recordAnswer(ctx, tx, chain.BroadcastOutcomeUnknown), nil
	}

	_, sendErr := adapter.BroadcastTransaction(ctx, &types.SignedTx{ChainID: tx.Chain, RawBytes: raw, TxHash: tx.TxHash})
	switch {
	case sendErr == nil:
		return s.recordAnswer(ctx, tx, chain.BroadcastOutcomeAccepted), nil
	case errors.Is(sendErr, chain.ErrUnknownOutcome):
		// The adapter already weighed the answer (a TRON duplicate no endpoint has).
		return s.recordAnswer(ctx, tx, chain.BroadcastOutcomeUnknown), nil
	case chain.RequestNotSent(sendErr) || errors.Is(sendErr, chain.ErrRateLimited):
		return nil, fmt.Errorf("%w: the same bytes reached no node: %w", ErrSendUnverified, sendErr)
	case answerHas(sendErr, alreadyHeldPhrases):
		return s.recordAnswer(ctx, tx, chain.BroadcastOutcomeAccepted), nil
	case answerHas(sendErr, neverValidPhrases):
		return nil, s.failNeverValid(ctx, tx, sendErr)
	}
	slog.Warn("withdrawal send never answered and its same bytes were refused; recorded as an unknown outcome for the tracker's proofs",
		"tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "refusal", chain.ClientText(sendErr))
	return s.recordAnswer(ctx, tx, chain.BroadcastOutcomeUnknown), nil
}

// keptSignedBytes opens the signed bytes the row keeps.
func (s *service) keptSignedBytes(tx *models.Transaction) ([]byte, bool) {
	payload := map[string]any{}
	_ = json.Unmarshal([]byte(tx.RawPayload), &payload)
	stored, _ := payload[chain.SignedTxPayloadKey].(string)
	if s.sealer == nil || strings.TrimSpace(stored) == "" {
		return nil, false
	}
	opened, err := s.sealer.Open(stored)
	if err != nil {
		return nil, false
	}
	raw, err := hex.DecodeString(strings.TrimSpace(opened))
	return raw, err == nil && len(raw) > 0
}

// failNeverValid fails a row whose bytes can never be valid and drops them.
func (s *service) failNeverValid(ctx context.Context, tx *models.Transaction, refusal error) error {
	refused := withoutSignedBytes(tx.RawPayload, chain.BroadcastOutcomeRefused)
	if err := s.retryWrite(ctx, tx, "mark never-valid withdrawal", func(txCtx context.Context) error {
		if err := s.txRepo.MarkFailed(txCtx, tx.ID, broadcastRefusedReason); err != nil {
			return err
		}
		return s.txRepo.RecordBroadcast(txCtx, tx.ID, tx.TxHash, refused)
	}); err != nil {
		return fmt.Errorf("%w: %w", ErrSendUnverified, err)
	}
	slog.Warn("withdrawal signed bytes can never be valid; the row is failed", "tx_id", tx.ID, "tx_hash", tx.TxHash,
		"chain", tx.Chain, "refusal", chain.ClientText(refusal))
	tx.Status, tx.RawPayload = string(types.TxStatusFailed), refused
	return fmt.Errorf("%w: %s", ErrSignedBytesInvalid, chain.ClientText(refusal))
}

// recordAnswer keeps outcome on the row (the signed bytes dropped). A write that
// fails is logged: the row is replayed all the same, and the next retry records it.
func (s *service) recordAnswer(ctx context.Context, tx *models.Transaction, outcome string) *models.Transaction {
	answered := withoutSignedBytes(tx.RawPayload, outcome)
	if err := s.retryWrite(ctx, tx, "record withdrawal answer", func(txCtx context.Context) error {
		return s.txRepo.RecordBroadcast(txCtx, tx.ID, tx.TxHash, answered)
	}); err != nil {
		slog.Error("withdrawal send settled but its answer could not be recorded", "tx_id", tx.ID, "tx_hash", tx.TxHash, "error", err)
		return tx
	}
	tx.RawPayload = answered
	return tx
}

// answerHas reports a node answer whose text (the error or its provider cause)
// holds one of phrases.
func answerHas(err error, phrases []string) bool {
	text := strings.ToLower(err.Error() + " " + chain.CauseText(err))
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

// withoutSignedBytes is rawPayload with outcome and without the signed bytes; every
// other key (the sender nonce and the head the tracker's proof reads) is kept.
func withoutSignedBytes(rawPayload, outcome string) string {
	kept := map[string]any{}
	_ = json.Unmarshal([]byte(rawPayload), &kept)
	delete(kept, chain.SignedTxPayloadKey)
	kept[chain.BroadcastOutcomePayloadKey] = outcome
	raw, _ := json.Marshal(kept)
	return string(raw)
}

// searchBlock is the transaction's block from every endpoint when the adapter can
// ask them all (chain.TransactionSearcher), else from the failover order.
func searchBlock(ctx context.Context, adapter types.Chain, txHash string) (uint64, error) {
	if searcher, ok := adapter.(chain.TransactionSearcher); ok {
		return searcher.SearchTransactionBlock(ctx, txHash)
	}
	return adapter.GetTransactionBlock(ctx, txHash)
}
