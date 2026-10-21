package deposit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// reorgListLimit bounds the unresolved reorganized deposits one listing reads.
const reorgListLimit = 500

// ErrDepositNotReorganized is a deposit an operator asked to fail that is not an
// open deposit with an unresolved reorganization.
var ErrDepositNotReorganized = errors.New("deposit: not an open deposit whose reorganization is unresolved")

// unresolvedReorgFinder lists the open deposits whose reorganization nothing has
// resolved (the transaction repository).
type unresolvedReorgFinder interface {
	FindUnresolvedReorgs(ctx context.Context, chainID string, limit int) ([]models.Transaction, error)
}

// ReorgedDepositsRequest is one deposits:reorgs run: the chain to list (every chain
// when empty) and, with Fail, the deposit an operator checked can never land, which
// Apply marks failed (reorged_out) with deposit.failed; without Apply nothing is
// written.
type ReorgedDepositsRequest struct {
	ChainID string
	Fail    uuid.UUID
	Apply   bool
}

// ReorgedDepositRow is one unresolved reorganized deposit.
type ReorgedDepositRow struct {
	TransactionID  uuid.UUID
	Chain          string
	TxHash         string
	BlockNumber    int64
	BlockHash      string
	ToAddress      string
	ExternalUserID string
	Asset          string
	Amount         string
	SeenAt         time.Time
	NextCheckAt    time.Time
}

// ReorgedDepositsReport is what a run found: the unresolved deposits, or the one
// asked to fail and whether it was (Failed).
type ReorgedDepositsReport struct {
	Rows   []ReorgedDepositRow
	Failed bool
}

// ReorgedDeposits lists the deposits whose block was reorganized away and that
// nothing resolved (no endpoint shows their transaction and the chain gives no
// proof it can never land), or fails one an operator checked: the same publish and
// guarded mark as the tracker's (failDeposit), only for an open deposit that still
// carries its reorganization mark.
func (s *Service) ReorgedDeposits(ctx context.Context, req ReorgedDepositsRequest) (ReorgedDepositsReport, error) {
	if req.Fail == uuid.Nil {
		finder, ok := s.txRepo.(unresolvedReorgFinder)
		if !ok {
			return ReorgedDepositsReport{}, fmt.Errorf("reorganized deposits cannot be listed")
		}
		rows, err := finder.FindUnresolvedReorgs(ctx, req.ChainID, reorgListLimit)
		if err != nil {
			return ReorgedDepositsReport{}, err
		}
		report := ReorgedDepositsReport{Rows: make([]ReorgedDepositRow, 0, len(rows))}
		for _, tx := range rows {
			report.Rows = append(report.Rows, reorgedRow(tx))
		}
		return report, nil
	}
	tx, err := s.txRepo.FindByID(ctx, req.Fail)
	if err != nil {
		return ReorgedDepositsReport{}, fmt.Errorf("deposit %s: %w", req.Fail, err)
	}
	open := tx.Status == string(types.TxStatusPending) || tx.Status == string(types.TxStatusConfirming)
	if tx.TxType != models.TxTypeDeposit || !open || payloadUnixTime(*tx, reorgSeenAtPayloadKey).IsZero() {
		return ReorgedDepositsReport{}, fmt.Errorf("deposit %s (%s, %s): %w", tx.ID, tx.TxType, tx.Status, ErrDepositNotReorganized)
	}
	report := ReorgedDepositsReport{Rows: []ReorgedDepositRow{reorgedRow(*tx)}}
	if !req.Apply {
		return report, nil
	}
	if err := s.failDeposit(ctx, *tx, models.DepositFailureReorgedOut); err != nil {
		return report, err
	}
	report.Failed = true
	return report, nil
}

func reorgedRow(tx models.Transaction) ReorgedDepositRow {
	return ReorgedDepositRow{
		TransactionID: tx.ID, Chain: tx.Chain, TxHash: tx.TxHash, BlockNumber: tx.BlockNumber, BlockHash: tx.BlockHash,
		ToAddress: tx.ToAddress, ExternalUserID: tx.ExternalUserID, Asset: tx.Asset, Amount: tx.Amount,
		SeenAt: payloadUnixTime(tx, reorgSeenAtPayloadKey), NextCheckAt: payloadUnixTime(tx, reorgNextCheckAtPayloadKey),
	}
}
