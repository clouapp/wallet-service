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

// ErrSendUnverified is a withdrawal row whose send was never answered and that
// cannot be settled now (the same bytes reached no node, or nothing proves what a
// refusal means): it is neither replayed as broadcast nor signed again. A later retry
// checks it again; withdrawals:reconcile settles it.
var ErrSendUnverified = errors.New("the withdrawal's send could not be verified yet")

// SendSettlement is what SettleUnanswered found. Sent is true when the transfer is
// on its way (an endpoint has it, or the same bytes were taken or got an unknown
// answer): the row is to be replayed. Otherwise the send never happened: the row is
// failed and its key released, so the withdrawal may be signed again.
type SendSettlement struct {
	Sent        bool
	Transaction *models.Transaction
}

// SettleUnanswered settles a withdrawal row recorded before its send whose send was
// never answered (broadcast_outcome pending: a crash between the insert and the
// answer, or a refusal whose mark failed), before a retry with its key replays it:
//
//  1. its hash on every endpoint: found (mined, or failed on chain) is sent;
//  2. the same signed bytes, kept in raw_payload, sent again (never a new
//     signature): taken (or already known) or an unknown answer is sent; reaching no
//     node is ErrSendUnverified;
//  3. a refusal is proof the send never happened only when the sender's nonce is
//     still free (EVM: nothing mined it; a node that held the bytes would have
//     answered "already known"): the row is failed. A spent nonce is an unknown
//     outcome, settled by the tracker's nonce proof; elsewhere the refusal proves
//     nothing and is ErrSendUnverified.
func (s *service) SettleUnanswered(ctx context.Context, tx *models.Transaction) (SendSettlement, error) {
	if tx == nil {
		return SendSettlement{}, fmt.Errorf("settle unanswered withdrawal: transaction is required")
	}
	adapter, err := s.registry.Chain(tx.Chain)
	if err != nil {
		return SendSettlement{}, fmt.Errorf("%w: %w", ErrSendUnverified, err)
	}
	block, err := searchBlock(ctx, adapter, tx.TxHash)
	var onChain *chain.TransactionFailedError
	switch {
	case errors.As(err, &onChain) || (err == nil && block > 0):
		return s.recordAnswer(ctx, tx, chain.BroadcastOutcomeAccepted)
	case err != nil:
		return SendSettlement{}, fmt.Errorf("%w: search %s: %v", ErrSendUnverified, tx.TxHash, chain.ClientText(err))
	}
	payload := map[string]any{}
	_ = json.Unmarshal([]byte(tx.RawPayload), &payload)
	signedHex, _ := payload[chain.SignedTxPayloadKey].(string)
	raw, decodeErr := hex.DecodeString(strings.TrimSpace(signedHex))
	if decodeErr != nil || len(raw) == 0 {
		return SendSettlement{}, fmt.Errorf("%w: the signed bytes of %s were not kept", ErrSendUnverified, tx.TxHash)
	}

	_, sendErr := adapter.BroadcastTransaction(ctx, &types.SignedTx{ChainID: tx.Chain, RawBytes: raw, TxHash: tx.TxHash})
	switch {
	case sendErr == nil:
		return s.recordAnswer(ctx, tx, chain.BroadcastOutcomeAccepted)
	case errors.Is(sendErr, chain.ErrUnknownOutcome):
		return s.recordAnswer(ctx, tx, chain.BroadcastOutcomeUnknown)
	case chain.RequestNotSent(sendErr) || errors.Is(sendErr, chain.ErrRateLimited):
		return SendSettlement{}, fmt.Errorf("%w: the same bytes reached no node: %v", ErrSendUnverified, chain.ClientText(sendErr))
	}
	spent, proven := minedNonceSpent(ctx, adapter, tx, payload)
	switch {
	case !proven:
		return SendSettlement{}, fmt.Errorf("%w: the same bytes were refused (%v) and nothing proves they were never taken",
			ErrSendUnverified, chain.ClientText(sendErr))
	case spent:
		return s.recordAnswer(ctx, tx, chain.BroadcastOutcomeUnknown)
	}
	refused := withoutSignedBytes(payload, chain.BroadcastOutcomeRefused)
	if err := s.retryWrite(ctx, tx, "mark never-sent withdrawal", func(txCtx context.Context) error {
		if err := s.txRepo.MarkFailed(txCtx, tx.ID, broadcastRefusedReason); err != nil {
			return err
		}
		return s.txRepo.RecordBroadcast(txCtx, tx.ID, tx.TxHash, refused)
	}); err != nil {
		return SendSettlement{}, fmt.Errorf("%w: %w", ErrSendUnverified, err)
	}
	slog.Warn("withdrawal never sent: the same bytes were refused with the sender nonce free; the withdrawal may be signed again",
		"tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "refusal", chain.ClientText(sendErr))
	tx.Status, tx.RawPayload = string(types.TxStatusFailed), refused
	return SendSettlement{Transaction: tx}, nil
}

// recordAnswer keeps outcome on the row (the signed bytes dropped): it is sent.
func (s *service) recordAnswer(ctx context.Context, tx *models.Transaction, outcome string) (SendSettlement, error) {
	payload := map[string]any{}
	_ = json.Unmarshal([]byte(tx.RawPayload), &payload)
	answered := withoutSignedBytes(payload, outcome)
	if err := s.retryWrite(ctx, tx, "record withdrawal answer", func(txCtx context.Context) error {
		return s.txRepo.RecordBroadcast(txCtx, tx.ID, tx.TxHash, answered)
	}); err != nil {
		// The transfer is on its way: replay it; the next retry records it again.
		slog.Error("withdrawal send settled but its answer could not be recorded", "tx_id", tx.ID, "tx_hash", tx.TxHash, "error", err)
	} else {
		tx.RawPayload = answered
	}
	return SendSettlement{Sent: true, Transaction: tx}, nil
}

// minedNonceSpent reads, for a chain that counts mined nonces (EVM), whether the
// row's sender nonce is spent; proven is false elsewhere or when it cannot be read.
func minedNonceSpent(ctx context.Context, adapter types.Chain, tx *models.Transaction, payload map[string]any) (spent, proven bool) {
	reader, ok := adapter.(chain.MinedNonceReader)
	nonce, hasNonce := payload[chain.SenderNoncePayloadKey].(float64)
	if !ok || !hasNonce || strings.TrimSpace(tx.FromAddress) == "" {
		return false, false
	}
	mined, err := reader.MinedNonce(ctx, tx.FromAddress)
	if err != nil {
		slog.Warn("read the sender's mined nonce", "tx_id", tx.ID, "chain", tx.Chain, "error", err)
		return false, false
	}
	return mined > uint64(nonce), true
}

// withoutSignedBytes is payload with outcome and without the signed bytes.
func withoutSignedBytes(payload map[string]any, outcome string) string {
	kept := make(map[string]any, len(payload))
	for key, value := range payload {
		if key != chain.SignedTxPayloadKey {
			kept[key] = value
		}
	}
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
