package deposit

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/macrowallets/waas/app/models"
)

const (
	// DefaultStaleOutboundAfter is how long an outbound transaction (withdrawal,
	// sweep, gas seed) may stay pending or confirming before the tracker warns about
	// it on each pass and the health check counts it.
	DefaultStaleOutboundAfter = 6 * time.Hour
	// staleOutboundCacheFor is how long the health check reuses its counts.
	staleOutboundCacheFor = time.Minute
	// staleOutboundMessage is the warning the tracker logs per unsettled transfer.
	staleOutboundMessage = "outbound transaction still unsettled; settle it with withdrawals:reconcile"
)

// staleOutboundCounter counts, per chain, the outbound transactions still pending
// or confirming that were recorded before olderThan (the transaction repository).
type staleOutboundCounter interface {
	CountStaleOutbound(ctx context.Context, olderThan time.Time) (map[string]int, error)
}

// reorgCounter counts, per chain, the open deposits whose block was reorganized
// away and that nothing has resolved yet (the transaction repository).
type reorgCounter interface {
	CountUnresolvedReorgs(ctx context.Context) (map[string]int, error)
}

// StaleOutboundHealth is what the health check reports of the outbound transfers
// nothing settled after After, and of the deposits whose block was reorganized
// away that nothing resolved yet (Reorged, reorg.go). Err is set when they could not
// be counted.
type StaleOutboundHealth struct {
	Counts       map[string]int
	Total        int
	After        time.Duration
	Reorged      map[string]int
	ReorgedTotal int
	Err          error
}

// SetStaleOutboundAfter sets how long an outbound transfer may stay unsettled; zero
// or less keeps DefaultStaleOutboundAfter.
func (s *Service) SetStaleOutboundAfter(after time.Duration) {
	if after > 0 {
		s.staleAfter = after
	}
}

// SetStaleOutboundAfterSource reads, at each use, a stored threshold that wins over
// the one SetStaleOutboundAfter set (the withdrawals platform setting); a zero or
// negative answer keeps that one.
func (s *Service) SetStaleOutboundAfterSource(source func(ctx context.Context) time.Duration) {
	s.staleAfterSource = source
}

func (s *Service) staleOutboundAfter(ctx context.Context) time.Duration {
	if s.staleAfterSource != nil && ctx != nil {
		if stored := s.staleAfterSource(ctx); stored > 0 {
			return stored
		}
	}
	if s.staleAfter > 0 {
		return s.staleAfter
	}
	return DefaultStaleOutboundAfter
}

// warnStaleOutbound logs each outbound transaction of pending recorded longer ago
// than the stale threshold: a transfer nothing settles (an unknown outcome the chain
// cannot prove, a node without its index) that an operator settles with
// withdrawals:reconcile.
func (s *Service) warnStaleOutbound(ctx context.Context, chainID string, pending []models.Transaction) {
	after, now := s.staleOutboundAfter(ctx), s.now()
	for _, tx := range pending {
		if !isOutboundTxType(tx.TxType) || tx.CreatedAt == nil {
			continue
		}
		age := now.Sub(tx.CreatedAt.StdTime())
		if age < after {
			continue
		}
		slog.Warn(staleOutboundMessage,
			"chain", chainID, "tx_id", tx.ID.String(), "tx_hash", tx.TxHash, "tx_type", tx.TxType,
			"status", tx.Status, "age", age.Round(time.Minute).String())
	}
}

// StaleOutboundHealth counts the outbound transfers left unsettled, per chain, for
// the health check. The counts are reused for a minute and counted one at a time,
// never under a lock (healthSnapshot): a check that finds a count in flight, or a
// count that fails, gets the last counts.
func (s *Service) StaleOutboundHealth(ctx context.Context) StaleOutboundHealth {
	now := s.now()
	return s.staleCache.get(ctx, now, staleOutboundCacheFor, healthRead[StaleOutboundHealth]{
		Read: func(ctx context.Context) (StaleOutboundHealth, error) {
			health := s.countStaleOutbound(ctx, now)
			return health, health.Err
		},
		Unavailable: func(err error) StaleOutboundHealth {
			return StaleOutboundHealth{After: s.staleOutboundAfter(ctx), Err: err}
		},
		Failure: "count stale outbound transactions",
		Busy:    fmt.Errorf("stale outbound transactions are being counted"),
	})
}

// countStaleOutbound counts under pendingHealthTimeout.
func (s *Service) countStaleOutbound(ctx context.Context, now time.Time) StaleOutboundHealth {
	health := StaleOutboundHealth{After: s.staleOutboundAfter(ctx)}
	counter, ok := s.txRepo.(staleOutboundCounter)
	if !ok {
		health.Err = fmt.Errorf("stale outbound transactions cannot be counted")
		return health
	}
	reorgs, ok := s.txRepo.(reorgCounter)
	if !ok {
		health.Err = fmt.Errorf("reorganized deposits cannot be counted")
		return health
	}
	countCtx, cancel := context.WithTimeout(ctx, pendingHealthTimeout)
	defer cancel()
	if health.Counts, health.Err = counter.CountStaleOutbound(countCtx, now.Add(-health.After)); health.Err != nil {
		return health
	}
	for _, count := range health.Counts {
		health.Total += count
	}
	if health.Reorged, health.Err = reorgs.CountUnresolvedReorgs(countCtx); health.Err != nil {
		return health
	}
	for _, count := range health.Reorged {
		health.ReorgedTotal += count
	}
	return health
}
