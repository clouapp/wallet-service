// Package depositrecord records each transfer to a watched address as one deposit
// row. The deposit scanner and the provider ingest both record through it, so a
// transfer seen twice, or seen by both, keeps one row and one transaction id.
//
// A deposit is identified by (chain, tx_hash, log_index), the unique deposit index:
// log_index holds the transfer's position in its transaction, which both sources
// compute alike (types.DetectedTransfer.LogIndex). Rows recorded before positions
// existed hold types.NativeValuePosition whatever the transfer was, because the
// scanner kept one deposit per transaction.
package depositrecord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/pgerr"
	"github.com/macrowallets/waas/pkg/types"
)

// ErrClaimContended is a row recorded before positions that other writers kept
// moving while this one tried to claim it; the transfer is not recorded this time.
var ErrClaimContended = errors.New("depositrecord: the transaction's deposits kept changing while it was recorded")

// errClaimLost is a claim of an old row another writer moved first.
var errClaimLost = errors.New("depositrecord: the old row was moved by another writer")

// maxDecisions bounds how often Record decides again after losing a claim.
const maxDecisions = 3

// Store is the deposit rows of one transaction: read, inserted, and an old row moved
// to the position of the transfer it records, under a lock per transaction.
type Store interface {
	// Within runs fn in one database transaction; the queries made with the context
	// fn receives join it.
	Within(ctx context.Context, fn func(ctx context.Context) error) error
	// LockTransactionDeposits takes the lock of the chain transaction's deposits,
	// held until the database transaction of ctx ends.
	LockTransactionDeposits(ctx context.Context, chainID, txHash string) error
	// FindDepositsByChainAndTxHash returns every deposit row of the transaction.
	FindDepositsByChainAndTxHash(ctx context.Context, chainID, txHash string) ([]models.Transaction, error)
	// ClaimDepositPosition moves deposit id from types.NativeValuePosition to position
	// and reports false when the row no longer holds that value.
	ClaimDepositPosition(ctx context.Context, id uuid.UUID, position int) (bool, error)
	// FillDepositBlockHash writes blockHash on deposit id when it holds none.
	FillDepositBlockHash(ctx context.Context, id uuid.UUID, blockHash string) error
	Create(ctx context.Context, tx *models.Transaction) error
}

// Transfer is what tells one transfer of a transaction from another: its position,
// recipient, token contract ("" for the native asset) and base-unit amount.
type Transfer struct {
	Position int
	To       string
	Contract string
	Amount   string
}

// TransferOf is the transfer a deposit row records.
func TransferOf(row models.Transaction) Transfer {
	return Transfer{Position: row.LogIndex, To: row.ToAddress, Contract: row.TokenContract, Amount: row.Amount}
}

// Outcome is what Record did with a transfer.
type Outcome string

const (
	// Created inserted the deposit row.
	Created Outcome = "created"
	// SamePosition found the transfer recorded at its position.
	SamePosition Outcome = "same_position"
	// PositionTaken found another transfer at the position: the sources numbered the
	// transaction differently, or two transfers share one position (two contract-made
	// native transfers of an EVM transaction). Nothing is inserted.
	PositionTaken Outcome = "position_taken"
	// OtherBlock found the transfer recorded from another block: the transaction was
	// mined again after a reorganization, where block-wide positions (EVM and TRON log
	// indexes) differ.
	OtherBlock Outcome = "other_block"
	// ClaimedUnpositioned moved the row this source recorded for the same transfer
	// before positions existed to the transfer's position, keeping its id.
	ClaimedUnpositioned Outcome = "claimed_unpositioned"
	// OtherSource found the same transfer recorded by the other source (scanner or
	// provider push) at a position none of this source's transfers has.
	OtherSource Outcome = "other_source"
	// OtherPosition found the same transfer recorded by this source at a position it
	// no longer reports for the transaction: a deposit the reorganization check moved
	// to the block its transaction was mined in again, with the old block's log index.
	// Decided only when the source passes its transfers.
	OtherPosition Outcome = "other_position"
	// Concurrent lost the insert or the claim to a writer that does not take the lock
	// (unique violation).
	Concurrent Outcome = "concurrent"
)

// Record inserts row unless a deposit row of its transaction already records the
// same transfer. siblings are the transfers of the same transaction the source
// reports with row (row's own included or not): a row of the other source that is one
// of them, at its position, is not taken for row. Each call holds the transaction's
// lock, so the scanner and the push never decide on the same transaction at once. It
// fails only when the rows cannot be read or written.
func Record(ctx context.Context, store Store, row *models.Transaction, siblings []Transfer) (Outcome, error) {
	if row == nil {
		return "", fmt.Errorf("deposit row is required")
	}
	for decision := 0; decision < maxDecisions; decision++ {
		var outcome Outcome
		err := store.Within(ctx, func(ctx context.Context) error {
			if err := store.LockTransactionDeposits(ctx, row.Chain, row.TxHash); err != nil {
				return fmt.Errorf("lock the deposits of tx %s: %w", row.TxHash, err)
			}
			var err error
			outcome, err = decide(ctx, store, row, siblings)
			return err
		})
		switch {
		case err == nil:
			return outcome, nil
		case errors.Is(err, errClaimLost):
			continue
		case pgerr.IsUniqueViolation(err):
			return Concurrent, nil
		default:
			return "", err
		}
	}
	return "", ErrClaimContended
}

// decide reads the transaction's deposits, under its lock, and writes row when none
// of them is its transfer. A recorded row that is the transfer and lacks the block
// hash row has, at the same height, gets it.
func decide(ctx context.Context, store Store, row *models.Transaction, siblings []Transfer) (Outcome, error) {
	recorded, err := store.FindDepositsByChainAndTxHash(ctx, row.Chain, row.TxHash)
	if err != nil {
		return "", fmt.Errorf("load recorded deposits of tx %s: %w", row.TxHash, err)
	}
	found := match(row, recorded, siblings)
	switch found.outcome {
	case "":
		if err := store.Create(ctx, row); err != nil {
			return "", fmt.Errorf("insert deposit of tx %s: %w", row.TxHash, err)
		}
		return Created, nil
	case ClaimedUnpositioned:
		claimed, err := store.ClaimDepositPosition(ctx, found.row.ID, row.LogIndex)
		if err != nil {
			return "", fmt.Errorf("move deposit %s to position %d: %w", found.row.ID, row.LogIndex, err)
		}
		if !claimed {
			return "", errClaimLost
		}
		return ClaimedUnpositioned, nil
	}
	if lacksHashOf(found.row, row) {
		if err := store.FillDepositBlockHash(ctx, found.row.ID, row.BlockHash); err != nil {
			return "", fmt.Errorf("fill the block hash of deposit %s: %w", found.row.ID, err)
		}
	}
	return found.outcome, nil
}

// lacksHashOf reports whether recorded, a row of the same transfer, has no block hash
// where row, at the same height, has one.
func lacksHashOf(recorded, row *models.Transaction) bool {
	return recorded != nil && recorded.BlockHash == "" && row.BlockHash != "" &&
		recorded.BlockNumber > 0 && recorded.BlockNumber == row.BlockNumber
}

// found is what match decided: the outcome, and the recorded row it is about (the row
// of the transfer, or the old row to claim); an empty outcome inserts.
type found struct {
	outcome Outcome
	row     *models.Transaction
}

// match decides from the transaction's recorded deposits whether the transfer is
// recorded, has an old row to claim, or must be inserted.
func match(row *models.Transaction, recorded []models.Transaction, siblings []Transfer) found {
	mine := TransferOf(*row)
	for i := range recorded {
		if recorded[i].LogIndex == row.LogIndex {
			if sameTransfer(TransferOf(recorded[i]), mine) {
				return found{SamePosition, &recorded[i]}
			}
			return found{PositionTaken, nil}
		}
	}
	for i := range recorded {
		if !sameBlock(recorded[i], *row) && sameTransfer(TransferOf(recorded[i]), mine) {
			return found{OtherBlock, nil}
		}
	}
	// A row at the native value position here is one recorded before positions: the
	// transfer's own position would have matched above.
	for i := range recorded {
		old := &recorded[i]
		if old.LogIndex == types.NativeValuePosition && old.Source == row.Source && sameTransfer(TransferOf(*old), mine) {
			return found{ClaimedUnpositioned, old}
		}
	}
	for i := range recorded {
		other := TransferOf(recorded[i])
		if !sameTransfer(other, mine) || reports(siblings, other) {
			continue
		}
		if recorded[i].Source != row.Source {
			return found{OtherSource, &recorded[i]}
		}
		if siblings != nil {
			return found{OtherPosition, &recorded[i]}
		}
	}
	return found{}
}

// reports tells whether the source reports transfer itself, at its position.
func reports(siblings []Transfer, transfer Transfer) bool {
	for _, sibling := range siblings {
		if sibling.Position == transfer.Position && sameTransfer(sibling, transfer) {
			return true
		}
	}
	return false
}

// sameTransfer reports whether two transfers credit the same amount of the same asset
// to the same address, whatever their positions. Addresses and contracts compare
// without case: a hex contract may be stored checksummed by one writer and
// lower-cased by another, and taking two transfers for one only leaves a deposit
// unrecorded, never recorded twice.
func sameTransfer(a, b Transfer) bool {
	return strings.EqualFold(a.To, b.To) && strings.EqualFold(a.Contract, b.Contract) && a.Amount == b.Amount
}

// sameBlock compares the block hashes when both rows have one, the heights when both
// have one, and otherwise cannot tell two blocks apart.
func sameBlock(a, b models.Transaction) bool {
	if a.BlockHash != "" && b.BlockHash != "" {
		return strings.EqualFold(a.BlockHash, b.BlockHash)
	}
	if a.BlockNumber > 0 && b.BlockNumber > 0 {
		return a.BlockNumber == b.BlockNumber
	}
	return true
}

// LogSkipped logs a transfer Record did not insert: a position another transfer holds
// and a reorganization at warning level, a claimed or another source's row at info
// level, the usual second sight of a transfer at debug level.
func LogSkipped(outcome Outcome, row *models.Transaction) {
	attrs := []any{"chain", row.Chain, "tx", row.TxHash, "log_index", row.LogIndex, "to", row.ToAddress, "source", row.Source, "outcome", string(outcome)}
	switch outcome {
	case PositionTaken:
		slog.Warn("deposit not recorded: another transfer of the transaction holds its position", attrs...)
	case OtherBlock:
		slog.Warn("transaction already recorded from another block (reorganized), its transfer is not recorded again", attrs...)
	case ClaimedUnpositioned:
		slog.Info("deposit recorded before transfer positions moved to its position", attrs...)
	case OtherSource:
		slog.Info("deposit already recorded by the other source at another position", attrs...)
	case OtherPosition:
		slog.Info("deposit already recorded by this source at a position it no longer reports (moved after a reorganization)", attrs...)
	default:
		slog.Debug("deposit already recorded", attrs...)
	}
}
