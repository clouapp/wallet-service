package deposit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// receiptVerificationPageSize is how many deposits one page of
// VerifyDepositReceipts reads from the database.
const receiptVerificationPageSize = 200

var (
	// ErrReceiptsUnsupported is a chain whose adapter reads no receipts: its blocks
	// never list a failed transaction as a native transfer (it is not EVM).
	ErrReceiptsUnsupported = errors.New("deposit: the chain adapter reads no transaction receipts")
	// ErrDepositReceiptsUnverified is a run that left deposits whose receipt it
	// could not read, or that it could not mark failed; run it again.
	ErrDepositReceiptsUnverified = errors.New("deposit: some deposits were not verified")
)

// DepositReceiptCheck selects the deposits VerifyDepositReceipts re-reads.
type DepositReceiptCheck struct {
	ChainID string
	// Since keeps the deposits recorded at or after it; zero reads them all.
	Since time.Time
	// Apply marks the deposits whose receipt failed as failed and publishes
	// deposit.failed; without it the run only reports them.
	Apply bool
}

// DepositReceiptRow is one confirmed deposit whose receipt failed or could not be
// verified.
type DepositReceiptRow struct {
	TransactionID  uuid.UUID
	TxHash         string
	ToAddress      string
	ExternalUserID string
	Asset          string
	// Amount is in base units of Asset.
	Amount      string
	BlockNumber int64
	// FailureCode is the on-chain result of a receipt that failed
	// (onchain_out_of_gas, onchain_reverted); empty when the receipt was not read.
	FailureCode string
	// Marked is a failed deposit this run marked failed, with deposit.failed sent.
	Marked bool
	// Err is why the receipt was not read, or why a failed deposit was not marked.
	Err error
}

// DepositReceiptReport is what one VerifyDepositReceipts run found and wrote.
type DepositReceiptReport struct {
	Chain string
	// Checked counts the confirmed native deposits read; Succeeded those whose
	// receipt succeeded.
	Checked   int
	Succeeded int
	// Failed lists the deposits whose receipt failed (with Err when the run could
	// not mark one); Unverified those whose receipt was not read.
	Failed     []DepositReceiptRow
	Unverified []DepositReceiptRow
	Marked     int
}

// VerifyDepositReceipts re-reads the receipt of every confirmed native deposit of
// a chain whose blocks include failed transactions (chain.ReceiptStatusReader)
// recorded since check.Since: the deposits a scanner confirmed before it read
// receipts. A deposit whose transaction was mined and failed moved no value; with
// Apply it is marked failed and deposit.failed is published (failDeposit), after
// which it is never selected again, so a second run changes nothing. It reads one
// receipt per deposit (two for a failed one) and never signs or broadcasts.
// Deposits whose receipt was not read, or that could not be marked, are reported,
// and the run returns ErrDepositReceiptsUnverified after trying every deposit.
func (s *Service) VerifyDepositReceipts(ctx context.Context, check DepositReceiptCheck) (DepositReceiptReport, error) {
	chainID := strings.TrimSpace(check.ChainID)
	report := DepositReceiptReport{Chain: chainID}
	if chainID == "" {
		return report, errors.New("verify deposit receipts: chain is required")
	}
	adapter, err := s.registry.Chain(chainID)
	if err != nil {
		return report, fmt.Errorf("chain %s: %w", chainID, err)
	}
	if _, ok := adapter.(chain.ReceiptStatusReader); !ok {
		return report, fmt.Errorf("chain %s: %w", chainID, ErrReceiptsUnsupported)
	}
	for after := uuid.Nil; ; {
		rows, err := s.txRepo.FindConfirmedNativeDeposits(ctx, chainID, check.Since, after, receiptVerificationPageSize)
		if err != nil {
			return report, err
		}
		for _, tx := range rows {
			if err := ctx.Err(); err != nil {
				return report, err
			}
			s.verifyDepositReceipt(ctx, adapter, tx, check.Apply, &report)
		}
		if len(rows) < receiptVerificationPageSize {
			return report, report.unverifiedError()
		}
		after = rows[len(rows)-1].ID
	}
}

func (s *Service) verifyDepositReceipt(ctx context.Context, adapter types.Chain, tx models.Transaction, apply bool, report *DepositReceiptReport) {
	report.Checked++
	row := DepositReceiptRow{
		TransactionID: tx.ID, TxHash: tx.TxHash, ToAddress: tx.ToAddress, ExternalUserID: tx.ExternalUserID,
		Asset: tx.Asset, Amount: tx.Amount, BlockNumber: tx.BlockNumber,
	}
	failureCode, err := nativeDepositReceipt(ctx, adapter, tx)
	if err != nil {
		row.Err = err
		report.Unverified = append(report.Unverified, row)
		return
	}
	if failureCode == "" {
		report.Succeeded++
		return
	}
	row.FailureCode = failureCode
	if apply {
		if err := s.failDeposit(ctx, tx, failureCode); err != nil {
			row.Err = err
		} else {
			row.Marked = true
			report.Marked++
		}
	}
	report.Failed = append(report.Failed, row)
}

// unverifiedError is ErrDepositReceiptsUnverified when a deposit was not read or a
// failed one was not marked.
func (r DepositReceiptReport) unverifiedError() error {
	unmarked := 0
	for _, row := range r.Failed {
		if row.Err != nil {
			unmarked++
		}
	}
	if len(r.Unverified) == 0 && unmarked == 0 {
		return nil
	}
	return fmt.Errorf("%s: %d receipts not read, %d failed deposits not marked: %w", r.Chain, len(r.Unverified), unmarked, ErrDepositReceiptsUnverified)
}
