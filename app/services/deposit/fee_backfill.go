package deposit

import (
	"context"
	"fmt"

	"github.com/macrowallets/waas/app/services/chain"
)

// feeBackfillMaxRows bounds one backfill pass per chain; run again for more.
const feeBackfillMaxRows = 500

// FeeBackfillRow is one confirmed outbound transaction whose paid fee was missing.
type FeeBackfillRow struct {
	Chain  string
	TxType string
	TxHash string
	// Fee is the paid fee in native base units; empty when Err is set.
	Fee string
	Err error
}

// FeeBackfillReport lists what a pass found and, when applied, wrote.
type FeeBackfillReport struct {
	Rows        []FeeBackfillRow
	Written     int
	Unsupported []string
}

// BackfillPaidFees reads from the chain the fee paid by every confirmed withdrawal,
// sweep and gas seed of chainIDs whose fee is empty, and with apply writes it. Rows
// that already have a fee are never selected, so a second pass changes nothing.
// Nothing is signed or broadcast.
func (s *Service) BackfillPaidFees(ctx context.Context, chainIDs []string, apply bool) (FeeBackfillReport, error) {
	var report FeeBackfillReport
	for _, chainID := range chainIDs {
		adapter, err := s.registry.Chain(chainID)
		if err != nil {
			return report, fmt.Errorf("chain %s: %w", chainID, err)
		}
		reader, ok := adapter.(chain.TransactionFeeReader)
		if !ok {
			report.Unsupported = append(report.Unsupported, chainID)
			continue
		}
		rows, err := s.txRepo.FindConfirmedOutboundWithoutFee(ctx, chainID, feeBackfillMaxRows)
		if err != nil {
			return report, fmt.Errorf("list %s transactions without fee: %w", chainID, err)
		}
		for _, tx := range rows {
			row := FeeBackfillRow{Chain: chainID, TxType: tx.TxType, TxHash: tx.TxHash}
			fee, err := reader.TransactionFee(ctx, tx.TxHash)
			if err != nil {
				row.Err = err
				report.Rows = append(report.Rows, row)
				continue
			}
			row.Fee = fee.String()
			if apply {
				if err := s.txRepo.SetFee(ctx, tx.ID, row.Fee); err != nil {
					return report, fmt.Errorf("write fee of %s: %w", tx.ID, err)
				}
				report.Written++
			}
			report.Rows = append(report.Rows, row)
		}
	}
	return report, nil
}
