package deposit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// transactions.raw_payload keys of a deposit whose block was reorganized away and
// that nothing has resolved yet: when the tracker first saw it (unix seconds; the
// health check counts the open deposits that carry it) and when it looks again.
const (
	reorgSeenAtPayloadKey      = "reorg_seen_at"
	reorgNextCheckAtPayloadKey = "reorg_next_check_at"
)

const (
	// reorgRecheckMin and reorgRecheckMax bound the wait before an unresolved
	// deposit is looked at again: the time since it was first seen, so the checks
	// space out (1 min, 1 min, 2 min, 4 min, ...) up to an hour.
	reorgRecheckMin = time.Minute
	reorgRecheckMax = time.Hour
	// reorgSpendLookback is how far under a deposit's recorded block the nonce proof
	// starts: the sender's nonce was unspent there on every fork of a reorganization
	// shallower than this. A deeper one proves nothing and is left to the operator.
	reorgSpendLookback = 64
)

// reorgPass is the reorganization check of one tracker pass over one chain. The
// hash of a height and the transfers of a block are read at most once each, so the
// deposits of one block share the reads; a read that fails is kept for the pass
// too. reader is nil on a chain whose blocks are final when they are recorded (XRP
// validated ledgers) or that cannot read block hashes.
type reorgPass struct {
	adapter types.Chain
	reader  chain.BlockHashReader
	hashes  map[uint64]canonicalHash
	blocks  map[uint64]scannedBlock
}

type canonicalHash struct {
	hash string
	err  error
}

type scannedBlock struct {
	transfers []types.DetectedTransfer
	err       error
}

func newReorgPass(adapter types.Chain) *reorgPass {
	reader, _ := adapter.(chain.BlockHashReader)
	return &reorgPass{adapter: adapter, reader: reader, hashes: map[uint64]canonicalHash{}, blocks: map[uint64]scannedBlock{}}
}

// checks reports whether tx is a deposit whose recorded block can be compared with
// the chain's: the adapter reads block hashes and the row has a block and its hash
// (some ingest providers record none). A deposit the scanner recorded from a block
// that was final when it was read (Solana scans finalized slots only) is not read
// again; one the ingest recorded is.
func (p *reorgPass) checks(tx models.Transaction) bool {
	if p.reader == nil || tx.TxType != models.TxTypeDeposit || tx.BlockNumber <= 0 || strings.TrimSpace(tx.BlockHash) == "" {
		return false
	}
	_, finalScans := p.adapter.(solanaNetwork)
	return !finalScans || tx.Source != models.TxSourceChain
}

func (p *reorgPass) hashAt(ctx context.Context, height uint64) (string, error) {
	if got, ok := p.hashes[height]; ok {
		return got.hash, got.err
	}
	hash, err := p.reader.BlockHashAt(ctx, height)
	p.hashes[height] = canonicalHash{hash: hash, err: err}
	return hash, err
}

func (p *reorgPass) transfersOf(ctx context.Context, block uint64) ([]types.DetectedTransfer, error) {
	if got, ok := p.blocks[block]; ok {
		return got.transfers, got.err
	}
	transfers, err := p.adapter.ScanBlock(ctx, block)
	p.blocks[block] = scannedBlock{transfers: transfers, err: err}
	return transfers, err
}

// sameBlockHash compares block hashes like transaction hashes: hex in any case,
// base58 (Solana) exactly.
func sameBlockHash(a, b string) bool {
	return sameTxHash(strings.TrimSpace(a), strings.TrimSpace(b))
}

// depositBlockAllowsConfirmation is the reorganization check of a deposit, made on
// the pass that would confirm it (and on no other, so a deposit costs at most one
// hash read per pass, and none once confirmed): the deposit confirms only while the
// chain's block at its recorded height is the block it was recorded in. A hash that
// cannot be read leaves it unconfirmed that pass. A hash that matches again clears
// the deposit's reorganization mark. A hash that differs means its block was
// reorganized away: relocateDeposit decides, and the deposit is not confirmed that
// pass. An unresolved deposit is not looked at before its next check.
func (s *Service) depositBlockAllowsConfirmation(ctx context.Context, pass *reorgPass, tx models.Transaction) bool {
	if !pass.checks(tx) {
		return true
	}
	if s.now().Before(payloadUnixTime(tx, reorgNextCheckAtPayloadKey)) {
		return false
	}
	canonical, err := pass.hashAt(ctx, recordedBlock(tx))
	if err != nil {
		slog.Warn("deposit not confirmed this pass: the hash of its block was not read", "tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "block", tx.BlockNumber, "error", err)
		return false
	}
	if sameBlockHash(canonical, tx.BlockHash) {
		s.clearReorgMark(ctx, tx)
		return true
	}
	slog.Warn("deposit's block was reorganized away; locating its transaction",
		"tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "block", tx.BlockNumber, "recorded_hash", tx.BlockHash, "canonical_hash", canonical)
	s.relocateDeposit(ctx, pass, tx)
	return false
}

// relocateDeposit decides about a deposit whose block was reorganized away. The
// transaction is looked up on every endpoint the adapter can ask
// (chain.TransactionSearcher), else in the failover order:
//   - in a block that credits the deposit as recorded: the deposit moves there and
//     confirms later, from that block's depth (moveDeposit);
//   - mined and failed on chain: failed with the on-chain code;
//   - in no block: only a positive proof that it can never land fails it
//     (proveLoss); anything else leaves it unresolved, unconfirmed and counted, for
//     a later check or the operator (deposits:reorgs).
//
// It is never failed for a transaction the endpoints merely do not show: one back in
// the mempool lands later, and the scanner never records a transaction twice.
func (s *Service) relocateDeposit(ctx context.Context, pass *reorgPass, tx models.Transaction) {
	block, err := locateTransaction(ctx, pass.adapter, tx.TxHash)
	var onChain *chain.TransactionFailedError
	switch {
	case errors.As(err, &onChain):
		s.failReorganized(ctx, tx, onChainFailureCode(onChain.Result))
	case err != nil:
		s.markReorgUnresolved(ctx, tx, fmt.Sprintf("the transaction lookup failed: %v", err))
	case block > 0:
		if moveErr := s.moveDeposit(ctx, pass, tx, block); moveErr != nil {
			s.markReorgUnresolved(ctx, tx, moveErr.Error())
		}
	default:
		s.proveLoss(ctx, pass, tx)
	}
}

func locateTransaction(ctx context.Context, adapter types.Chain, txHash string) (uint64, error) {
	if searcher, ok := adapter.(chain.TransactionSearcher); ok {
		return searcher.SearchTransactionBlock(ctx, txHash)
	}
	return adapter.GetTransactionBlock(ctx, txHash)
}

// proveLoss decides about a deposit whose transaction no endpoint shows in a block.
// On EVM the scan kept the sender and nonce of its transaction (raw_payload):
// NonceSpend finds, at a final depth, the transaction that spent that nonce. Another
// one is the proof that ours can never land: the deposit fails reorged_out. Ours
// (an endpoint without a transaction index) moves the deposit to its block. A nonce
// not spent yet, a read that fails, or a chain or row without a proof leaves the
// deposit unresolved.
func (s *Service) proveLoss(ctx context.Context, pass *reorgPass, tx models.Transaction) {
	reader, canProve := pass.adapter.(chain.NonceSpendReader)
	sender, nonce, known := depositSender(tx)
	if !canProve || !known {
		s.markReorgUnresolved(ctx, tx, "the transaction is in no block and nothing proves it can never land; settle it with deposits:reorgs")
		return
	}
	block := recordedBlock(tx)
	spend, err := reader.NonceSpend(ctx, tx.TxHash, sender, nonce, block-min(block, reorgSpendLookback))
	switch {
	case err != nil:
		s.markReorgUnresolved(ctx, tx, fmt.Sprintf("the nonce proof failed: %v", err))
	case !spend.Spent:
		s.markReorgUnresolved(ctx, tx, "the sender's nonce is not spent at a final depth: the transaction may still land")
	case spend.Ours && spend.Result != "":
		s.failReorganized(ctx, tx, onChainFailureCode(spend.Result))
	case spend.Ours:
		if moveErr := s.moveDeposit(ctx, pass, tx, spend.Block); moveErr != nil {
			s.markReorgUnresolved(ctx, tx, moveErr.Error())
		}
	default:
		slog.Warn("reorganized deposit can never land: another transaction spent its sender's nonce",
			"tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "sender", sender, "nonce", nonce, "spent_in_block", spend.Block)
		s.failReorganized(ctx, tx, models.DepositFailureReorgedOut)
	}
}

// depositSender is the sender and nonce the scan kept for a deposit's transaction.
func depositSender(tx models.Transaction) (string, uint64, bool) {
	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(tx.RawPayload), &payload) != nil {
		return "", 0, false
	}
	var sender string
	var nonce uint64
	if json.Unmarshal(payload[chain.SenderPayloadKey], &sender) != nil || strings.TrimSpace(sender) == "" ||
		json.Unmarshal(payload[chain.SenderNoncePayloadKey], &nonce) != nil {
		return "", 0, false
	}
	return sender, nonce, true
}

// depositPayload is the raw_payload a scanned deposit is recorded with: the sender
// and nonce of its transaction when the chain reports them (EVM), which prove a
// reorganized-out deposit can never land; "{}" otherwise.
func depositPayload(transfer types.DetectedTransfer) string {
	if transfer.SenderNonce == nil || strings.TrimSpace(transfer.Sender) == "" {
		return "{}"
	}
	raw, err := json.Marshal(map[string]any{chain.SenderPayloadKey: transfer.Sender, chain.SenderNoncePayloadKey: *transfer.SenderNonce})
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// moveDeposit records the block a reorganization moved a deposit's transaction to
// (its height may be the old one: the competing block often includes the same
// transaction). The block is read like the scanner reads it and must credit the
// address the asset and amount the deposit recorded; its hash is the one the next
// pass compares, and the deposit confirms from that block's depth (its stored
// confirmation count goes down when the new block is younger). A block that does
// not hold the deposit as recorded (endpoints that disagree, a transaction that now
// credits another amount or address) changes nothing and is the error.
func (s *Service) moveDeposit(ctx context.Context, pass *reorgPass, tx models.Transaction, block uint64) error {
	transfers, err := pass.transfersOf(ctx, block)
	if err != nil {
		return fmt.Errorf("block %d, which holds the transaction, was not read: %w", block, err)
	}
	hash := ""
	for _, transfer := range transfers {
		if creditsDeposit(transfer, tx) {
			hash = strings.TrimSpace(transfer.BlockHash)
			break
		}
	}
	if hash == "" {
		return fmt.Errorf("block %d does not credit the deposit as it was recorded", block)
	}
	if block == recordedBlock(tx) && sameBlockHash(hash, tx.BlockHash) {
		return fmt.Errorf("block %d is reported with the recorded hash again: the endpoints disagree", block)
	}
	if err := s.txRepo.SetBlock(ctx, tx.ID, block, hash); err != nil {
		return fmt.Errorf("record block %d: %w", block, err)
	}
	s.clearReorgMark(ctx, tx)
	slog.Warn("reorganized deposit moved to another block; it confirms from there",
		"tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "from_block", tx.BlockNumber, "to_block", block, "block_hash", hash)
	return nil
}

// recordedBlock is the block a row was recorded in, 0 for none.
func recordedBlock(tx models.Transaction) uint64 {
	if tx.BlockNumber <= 0 {
		return 0
	}
	return uint64(tx.BlockNumber)
}

// creditsDeposit reports whether transfer is the credit deposit tx recorded: the
// same transaction, recipient, asset and amount.
func creditsDeposit(transfer types.DetectedTransfer, tx models.Transaction) bool {
	if !sameTxHash(transfer.TxHash, tx.TxHash) || !strings.EqualFold(transfer.To, tx.ToAddress) || transfer.Amount == nil || transfer.Amount.String() != tx.Amount {
		return false
	}
	contract := ""
	if transfer.Token != nil {
		contract = transfer.Token.Contract
	}
	return strings.EqualFold(strings.TrimSpace(contract), strings.TrimSpace(tx.TokenContract))
}

// markReorgUnresolved leaves a reorganized deposit unconfirmed and marks it in
// raw_payload: when it was first seen (kept) and its next check, the time since then
// (at least reorgRecheckMin, at most reorgRecheckMax) from now. The health check
// counts the open deposits so marked.
func (s *Service) markReorgUnresolved(ctx context.Context, tx models.Transaction, reason string) {
	now := s.now()
	seen := payloadUnixTime(tx, reorgSeenAtPayloadKey)
	if seen.IsZero() {
		seen = now
	}
	next := now.Add(min(max(now.Sub(seen), reorgRecheckMin), reorgRecheckMax))
	s.editPayload(ctx, tx, map[string]any{reorgSeenAtPayloadKey: seen.Unix(), reorgNextCheckAtPayloadKey: next.Unix()})
	slog.Warn("reorganized deposit unresolved; it stays unconfirmed",
		"tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "block", tx.BlockNumber, "reason", reason,
		"seen_at", seen.UTC().Format(time.RFC3339), "next_check_at", next.UTC().Format(time.RFC3339))
}

// clearReorgMark drops the reorganization mark of a deposit resolved by a matching
// hash or a move; a row without one is not written.
func (s *Service) clearReorgMark(ctx context.Context, tx models.Transaction) {
	if payloadUnixTime(tx, reorgSeenAtPayloadKey).IsZero() && payloadUnixTime(tx, reorgNextCheckAtPayloadKey).IsZero() {
		return
	}
	s.editPayload(ctx, tx, nil, reorgSeenAtPayloadKey, reorgNextCheckAtPayloadKey)
}

// failReorganized fails a reorganized deposit with code; another process that
// settled it meanwhile wins.
func (s *Service) failReorganized(ctx context.Context, tx models.Transaction, code string) {
	err := s.failDeposit(ctx, tx, code)
	switch {
	case errors.Is(err, models.ErrTransactionSettled):
		slog.Info("reorganized deposit settled by another process meanwhile", "tx_id", tx.ID, "chain", tx.Chain, "error", err)
	case err != nil:
		slog.Error("reorganized deposit was not marked failed; retrying next pass", "tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "failure_code", code, "error", err)
	}
}
