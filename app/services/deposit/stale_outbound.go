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

// staleOutboundCache keeps the last health counts and the count in flight.
type staleOutboundCache struct {
	mu     sync.Mutex
	at     time.Time
	have   bool
	health StaleOutboundHealth
	// counting is closed when the count in flight ends; nil when none is.
	counting chan struct{}
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
// the health check. The counts are reused for a minute. One count runs at a time and
// the lock is never held during it: a check that finds a count in flight waits for it
// at most pendingHealthTimeout, then serves the last counts. A count that fails
// serves the last counts too (logged); with none yet, the failure is reported, never
// a failure of the check.
func (s *Service) StaleOutboundHealth(ctx context.Context) StaleOutboundHealth {
	cache := &s.staleCache
	cache.mu.Lock()
	now := s.now()
	if cache.have && now.Sub(cache.at) < staleOutboundCacheFor {
		defer cache.mu.Unlock()
		return cache.health
	}
	if inFlight := cache.counting; inFlight != nil {
		cache.mu.Unlock()
		timer := time.NewTimer(pendingHealthTimeout)
		defer timer.Stop()
		select {
		case <-inFlight:
		case <-timer.C:
		case <-ctx.Done():
		}
		return s.lastStaleOutbound(fmt.Errorf("stale outbound transactions are being counted"))
	}
	done := make(chan struct{})
	cache.counting = done
	cache.mu.Unlock()

	health := s.countStaleOutbound(ctx, now)

	cache.mu.Lock()
	cache.counting = nil
	close(done)
	if health.Err == nil {
		cache.at, cache.have, cache.health = now, true, health
	}
	cache.mu.Unlock()
	if health.Err != nil {
		slog.Error("count stale outbound transactions", "error", health.Err)
		return s.lastStaleOutbound(health.Err)
	}
	return health
}

// lastStaleOutbound is the last counts, or err with none yet.
func (s *Service) lastStaleOutbound(err error) StaleOutboundHealth {
	s.staleCache.mu.Lock()
	defer s.staleCache.mu.Unlock()
	if s.staleCache.have {
		return s.staleCache.health
	}
	return StaleOutboundHealth{After: s.staleOutboundAfter(), Err: err}
}

// countStaleOutbound counts under pendingHealthTimeout.
func (s *Service) countStaleOutbound(ctx context.Context, now time.Time) StaleOutboundHealth {
	health := StaleOutboundHealth{After: s.staleOutboundAfter()}
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
