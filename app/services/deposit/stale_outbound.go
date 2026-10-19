package deposit

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
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

// StaleOutboundHealth is what the health check reports of the outbound transfers
// nothing settled after After. Err is set when they could not be counted.
type StaleOutboundHealth struct {
	Counts map[string]int
	Total  int
	After  time.Duration
	Err    error
}

// staleOutboundCache keeps the last health counts.
type staleOutboundCache struct {
	mu     sync.Mutex
	at     time.Time
	health StaleOutboundHealth
}

// SetStaleOutboundAfter sets how long an outbound transfer may stay unsettled; zero
// or less keeps DefaultStaleOutboundAfter.
func (s *Service) SetStaleOutboundAfter(after time.Duration) {
	if after > 0 {
		s.staleAfter = after
	}
}

func (s *Service) staleOutboundAfter() time.Duration {
	if s.staleAfter > 0 {
		return s.staleAfter
	}
	return DefaultStaleOutboundAfter
}

// warnStaleOutbound logs each outbound transaction of pending recorded longer ago
// than the stale threshold: a transfer nothing settles (an unknown outcome the chain
// cannot prove, a node without its index) that an operator settles with
// withdrawals:reconcile.
func (s *Service) warnStaleOutbound(chainID string, pending []models.Transaction) {
	after, now := s.staleOutboundAfter(), s.now()
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
// the health check. The counts are reused for a minute; a store that cannot count
// them is reported, never a failure of the check.
func (s *Service) StaleOutboundHealth(ctx context.Context) StaleOutboundHealth {
	s.staleCache.mu.Lock()
	defer s.staleCache.mu.Unlock()
	now := s.now()
	if !s.staleCache.at.IsZero() && now.Sub(s.staleCache.at) < staleOutboundCacheFor {
		return s.staleCache.health
	}
	health := StaleOutboundHealth{After: s.staleOutboundAfter()}
	counter, ok := s.txRepo.(staleOutboundCounter)
	if !ok {
		health.Err = fmt.Errorf("stale outbound transactions cannot be counted")
		return health
	}
	countCtx, cancel := context.WithTimeout(ctx, pendingHealthTimeout)
	defer cancel()
	health.Counts, health.Err = counter.CountStaleOutbound(countCtx, now.Add(-health.After))
	for _, count := range health.Counts {
		health.Total += count
	}
	if health.Err != nil {
		slog.Error("count stale outbound transactions", "error", health.Err)
		return health
	}
	s.staleCache.at, s.staleCache.health = now, health
	return health
}
