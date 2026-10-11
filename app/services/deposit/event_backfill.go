package deposit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/macrowallets/waas/pkg/types"
)

const (
	// depositEventBackfillWindow bounds the backfill to the deposits confirmed in the
	// last two days; an older gap is repaired by asking for a redelivery.
	depositEventBackfillWindow = 48 * time.Hour
	// depositEventBackfillGrace leaves a confirmation this recent to the publish that
	// follows it in the same tracker run.
	depositEventBackfillGrace = time.Minute
	// depositEventBackfillBatchSize bounds the deposits one run republishes.
	depositEventBackfillBatchSize = 50
)

// BackfillDepositEvents publishes deposit.confirmed again for the deposits the
// tracker confirmed but whose event never reached the outbox (the status is
// persisted first and a failed publish is only logged): the client credits a deposit
// only on deposit.confirmed, so a lost event is a deposit never credited.
//
// A deposit is repaired when it was confirmed between depositEventBackfillWindow
// and depositEventBackfillGrace ago (on chainIDs; none means every chain) and a
// webhook config that is active, subscribes to deposit.confirmed, may see the
// deposit's wallet and existed at the confirmation has no webhook_events row for
// (config, deposit.confirmed, deposit id). The event is republished stamped with
// the confirmation time, so only those configs receive it; the outbox's per-config
// unique index keeps a repeated run from delivering twice. It returns how many
// deposits were republished; one that fails is logged and retried on the next run.
func (s *Service) BackfillDepositEvents(ctx context.Context, chainIDs []string) (int, error) {
	if s.deposits == nil {
		return 0, errors.New("backfill deposit events: no deposit events publisher configured")
	}
	now := s.now()
	deposits, err := s.txRepo.FindConfirmedDepositsMissingEvent(ctx, string(types.EventDepositConfirmed),
		now.Add(-depositEventBackfillWindow), now.Add(-depositEventBackfillGrace), chainIDs, depositEventBackfillBatchSize)
	if err != nil {
		return 0, fmt.Errorf("backfill deposit events: %w", err)
	}

	republished := 0
	for _, tx := range deposits {
		if ctx.Err() != nil {
			return republished, ctx.Err()
		}
		if tx.ConfirmedAt == nil {
			continue
		}
		if err := s.deposits.Republish(ctx, types.EventDepositConfirmed, tx, *tx.ConfirmedAt); err != nil {
			slog.Error("backfill deposit event", "event_type", types.EventDepositConfirmed, "transaction_id", tx.ID, "error", err)
			continue
		}
		republished++
	}
	return republished, nil
}

// backfillDepositEvents is the confirmation tracker's pass over every chain.
func (s *Service) backfillDepositEvents(ctx context.Context) {
	if s.deposits == nil {
		return
	}
	republished, err := s.BackfillDepositEvents(ctx, nil)
	if err != nil {
		slog.Error("backfill deposit events", "error", err)
		return
	}
	if republished > 0 {
		slog.Info("deposit events backfilled", "count", republished)
	}
}
