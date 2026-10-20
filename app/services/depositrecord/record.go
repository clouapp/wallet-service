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

// ErrClaimContended is a row recorded before positions that other processes kept
// moving while this one tried to claim it; the transfer is not recorded this time.
var ErrClaimContended = errors.New("depositrecord: the transaction's deposits kept changing while it was recorded")

// maxDecisions bounds how often Record decides again after losing a claim.
const maxDecisions = 3

// Store is the deposit rows of one transaction: read, inserted, and an old row moved
// to the position of the transfer it records.
type Store interface {
	// FindDepositsByChainAndTxHash returns every deposit row of the transaction.
	FindDepositsByChainAndTxHash(ctx context.Context, chainID, txHash string) ([]models.Transaction, error)
	// ClaimDepositPosition moves deposit id from types.NativeValuePosition to position
	// and reports false when the row no longer holds that value.
	ClaimDepositPosition(ctx context.Context, id uuid.UUID, position int) (bool, error)
	Create(ctx context.Context, tx *models.Transaction) error
}

// Outcome is what Record did with a transfer.
type Outcome string

const (
	// Created inserted the deposit row.
	Created Outcome = "created"
	// SamePosition found a row of the transaction at the transfer's position.
	SamePosition Outcome = "same_position"
	// OtherBlock found the transaction's deposits recorded from another block: the
	// transaction was mined again after a reorganization, where block-wide positions
	// (EVM and TRON log indexes) differ, and its transfers are already recorded.
	OtherBlock Outcome = "other_block"
	// ClaimedUnpositioned moved the row this source recorded for the same transfer
	// before positions existed to the transfer's position, keeping its id.
	ClaimedUnpositioned Outcome = "claimed_unpositioned"
	// OtherSource found the same transfer recorded by the other source (scanner or
	// provider push) at another position.
	OtherSource Outcome = "other_source"
	// Concurrent lost the insert or the claim to another process (unique violation).
	Concurrent Outcome = "concurrent"
)

// Record inserts row unless a deposit row of its transaction already records the
// same transfer. It fails only when the rows cannot be read or written.
func Record(ctx context.Context, store Store, row *models.Transaction) (Outcome, error) {
	if row == nil {
		return "", fmt.Errorf("deposit row is required")
	}
	for decision := 0; decision < maxDecisions; decision++ {
		recorded, err := store.FindDepositsByChainAndTxHash(ctx, row.Chain, row.TxHash)
		if err != nil {
			return "", fmt.Errorf("load recorded deposits of tx %s: %w", row.TxHash, err)
		}
		outcome, unpositioned := match(row, recorded)
		if outcome != "" {
			return outcome, nil
		}
		if unpositioned == nil {
			return create(ctx, store, row)
		}
		claimed, err := store.ClaimDepositPosition(ctx, unpositioned.ID, row.LogIndex)
		if err != nil {
			if pgerr.IsUniqueViolation(err) {
				return Concurrent, nil
			}
			return "", fmt.Errorf("move deposit %s to position %d: %w", unpositioned.ID, row.LogIndex, err)
		}
		if claimed {
			return ClaimedUnpositioned, nil
		}
	}
	return "", ErrClaimContended
}

func create(ctx context.Context, store Store, row *models.Transaction) (Outcome, error) {
	if err := store.Create(ctx, row); err != nil {
		if pgerr.IsUniqueViolation(err) {
			return Concurrent, nil
		}
		return "", fmt.Errorf("insert deposit of tx %s: %w", row.TxHash, err)
	}
	return Created, nil
}

// match decides from the transaction's recorded deposits: an outcome when the
// transfer is recorded, the old row to claim, or neither when it must be inserted.
func match(row *models.Transaction, recorded []models.Transaction) (Outcome, *models.Transaction) {
	for i := range recorded {
		if recorded[i].LogIndex == row.LogIndex {
			return SamePosition, nil
		}
	}
	for i := range recorded {
		if !sameBlock(recorded[i], *row) {
			return OtherBlock, nil
		}
	}
	// A row at the native value position here is one recorded before positions: the
	// transfer's own position would have matched above.
	for i := range recorded {
		old := &recorded[i]
		if old.LogIndex == types.NativeValuePosition && old.Source == row.Source && sameTransfer(*old, *row) {
			return "", old
		}
	}
	for i := range recorded {
		if recorded[i].Source != row.Source && sameTransfer(recorded[i], *row) {
			return OtherSource, nil
		}
	}
	return "", nil
}

// sameTransfer reports whether two rows credit the same amount of the same asset to
// the same address. Addresses and contracts compare without case: a hex contract may
// be stored checksummed by one writer and lower-cased by another, and taking two
// transfers for one only leaves a deposit unrecorded, never recorded twice.
func sameTransfer(a, b models.Transaction) bool {
	return strings.EqualFold(a.ToAddress, b.ToAddress) && strings.EqualFold(a.TokenContract, b.TokenContract) && a.Amount == b.Amount
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

// LogSkipped logs a transfer Record did not insert: a reorganization at warning
// level, a claimed or another source's row at info level, the usual second sight of a
// transfer at debug level.
func LogSkipped(outcome Outcome, row *models.Transaction) {
	attrs := []any{"chain", row.Chain, "tx", row.TxHash, "log_index", row.LogIndex, "to", row.ToAddress, "source", row.Source, "outcome", string(outcome)}
	switch outcome {
	case OtherBlock:
		slog.Warn("transaction already recorded from another block (reorganized), its transfer is not recorded again", attrs...)
	case ClaimedUnpositioned:
		slog.Info("deposit recorded before transfer positions moved to its position", attrs...)
	case OtherSource:
		slog.Info("deposit already recorded by the other source at another position", attrs...)
	default:
		slog.Debug("deposit already recorded", attrs...)
	}
}
