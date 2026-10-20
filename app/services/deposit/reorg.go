package deposit

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// reorgedOutSeenHeadPayloadKey is the head (a block number, in raw_payload) of the
// pass that first found a deposit's transaction in no block of the chain after its
// block was reorganized away. A reorganized transaction usually returns to the
// mempool and is mined again, and the scanner never records a transaction twice,
// so the deposit fails reorged_out only once the head is its required
// confirmations past that pass and the transaction is still in no block.
const reorgedOutSeenHeadPayloadKey = "reorged_out_seen_head"

// canonicalHashes reads the hash of the chain's block at a height at most once per
// tracker pass, so the deposits of one block share one read; a read that fails is
// kept for the pass too. Its reader is nil on a chain whose blocks are final when
// they are recorded (XRP validated ledgers) or that cannot read them.
type canonicalHashes struct {
	reader chain.BlockHashReader
	read   map[uint64]canonicalHash
}

type canonicalHash struct {
	hash string
	err  error
}

func newCanonicalHashes(adapter types.Chain) *canonicalHashes {
	reader, _ := adapter.(chain.BlockHashReader)
	return &canonicalHashes{reader: reader, read: map[uint64]canonicalHash{}}
}

// checks reports whether tx is a deposit whose recorded block can be compared with
// the chain's: the adapter reads block hashes and the row has a block and its hash
// (the inbound ingest of some providers records none).
func (h *canonicalHashes) checks(tx models.Transaction) bool {
	return h.reader != nil && tx.TxType == models.TxTypeDeposit && tx.BlockNumber > 0 && strings.TrimSpace(tx.BlockHash) != ""
}

func (h *canonicalHashes) at(ctx context.Context, height uint64) (string, error) {
	if got, ok := h.read[height]; ok {
		return got.hash, got.err
	}
	hash, err := h.reader.BlockHashAt(ctx, height)
	h.read[height] = canonicalHash{hash: hash, err: err}
	return hash, err
}

// depositBlockAllowsConfirmation is the reorganization check of a deposit, made on
// the pass that would confirm it (and on no other, so a deposit costs at most one
// hash read per pass, and none once confirmed): the deposit confirms only while the
// chain's block at its recorded height is the block it was recorded in. A hash that
// cannot be read leaves it unconfirmed that pass. A hash that differs means its
// block was reorganized away: relocateDeposit finds where its transaction is now,
// and the deposit is not confirmed that pass.
func (s *Service) depositBlockAllowsConfirmation(ctx context.Context, adapter types.Chain, hashes *canonicalHashes, tx models.Transaction, currentBlock uint64) bool {
	if !hashes.checks(tx) {
		return true
	}
	canonical, err := hashes.at(ctx, recordedBlock(tx))
	if err != nil {
		slog.Warn("deposit not confirmed this pass: the hash of its block was not read", "tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "block", tx.BlockNumber, "error", err)
		return false
	}
	if strings.EqualFold(strings.TrimSpace(canonical), strings.TrimSpace(tx.BlockHash)) {
		return true
	}
	slog.Warn("deposit's block was reorganized away; locating its transaction",
		"tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "block", tx.BlockNumber, "recorded_hash", tx.BlockHash, "canonical_hash", canonical)
	s.relocateDeposit(ctx, adapter, tx, currentBlock)
	return false
}

// relocateDeposit finds the block that holds the transaction of a deposit whose
// block was reorganized away: another block (moveDeposit, the deposit confirms from
// there), no block (depositGone), or a block where it failed on chain (the deposit
// fails with the on-chain code). A lookup that fails changes nothing.
func (s *Service) relocateDeposit(ctx context.Context, adapter types.Chain, tx models.Transaction, currentBlock uint64) {
	block, err := adapter.GetTransactionBlock(ctx, tx.TxHash)
	var onChain *chain.TransactionFailedError
	switch {
	case errors.As(err, &onChain):
		if failErr := s.failDeposit(ctx, tx, onChainFailureCode(onChain.Result)); failErr != nil {
			slog.Error("reorganized deposit whose transaction failed on chain was not marked failed; retrying next pass", "tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "error", failErr)
		}
	case err != nil:
		slog.Warn("reorganized deposit not located this pass", "tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "error", err)
	case block == 0:
		s.depositGone(ctx, tx, currentBlock)
	default:
		s.moveDeposit(ctx, adapter, tx, block)
	}
}

// moveDeposit records the block a reorganization moved a deposit's transaction to
// (its height may be the old one: the competing block often includes the same
// transaction). The block is read like the scanner reads it and must credit the
// address what the deposit recorded; its hash is the one the next pass compares.
// The deposit is confirmed later, from that block's depth. A block that does not
// hold the deposit (the endpoints disagree, or the transaction now credits another
// amount) changes nothing and is logged.
func (s *Service) moveDeposit(ctx context.Context, adapter types.Chain, tx models.Transaction, block uint64) {
	transfers, err := adapter.ScanBlock(ctx, block)
	if err != nil {
		slog.Warn("reorganized deposit: the block now holding its transaction was not read", "tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "block", block, "error", err)
		return
	}
	hash := ""
	for _, transfer := range transfers {
		if creditsDeposit(transfer, tx) {
			hash = strings.TrimSpace(transfer.BlockHash)
			break
		}
	}
	if hash == "" || (block == recordedBlock(tx) && strings.EqualFold(hash, tx.BlockHash)) {
		slog.Warn("reorganized deposit: the block reported to hold its transaction does not credit the deposit as recorded; retrying next pass",
			"tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "block", block, "block_hash", hash)
		return
	}
	if err := s.txRepo.SetBlock(ctx, tx.ID, block, hash); err != nil {
		slog.Error("record the block of a reorganized deposit", "tx_id", tx.ID, "error", err)
		return
	}
	if payloadUint64(tx, reorgedOutSeenHeadPayloadKey) != 0 {
		s.recordPayloadValue(ctx, tx, reorgedOutSeenHeadPayloadKey, 0)
	}
	slog.Warn("reorganized deposit moved to another block; it confirms from there",
		"tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "from_block", tx.BlockNumber, "to_block", block, "block_hash", hash)
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

// depositGone handles a deposit whose transaction no block holds after its block
// was reorganized away. The first pass records its head; the deposit fails
// reorged_out (deposit.failed published once, before the row is marked, as on an
// on-chain failure) when a pass at least its required confirmations later still
// finds the transaction in no block. Until then it stays unconfirmed, and a
// transaction mined again is moved instead.
func (s *Service) depositGone(ctx context.Context, tx models.Transaction, currentBlock uint64) {
	wait := uint64(max(tx.RequiredConfs, 1))
	seen := payloadUint64(tx, reorgedOutSeenHeadPayloadKey)
	if seen == 0 {
		s.recordPayloadValue(ctx, tx, reorgedOutSeenHeadPayloadKey, currentBlock)
		slog.Warn("reorganized deposit's transaction is in no block; it fails reorged_out unless mined again",
			"tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "head", currentBlock, "fails_at_head", currentBlock+wait)
		return
	}
	if currentBlock < seen+wait {
		return
	}
	if err := s.failDeposit(ctx, tx, models.DepositFailureReorgedOut); err != nil {
		slog.Error("reorganized-out deposit was not marked failed; retrying next pass", "tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "error", err)
	}
}
