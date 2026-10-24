package deposit

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/macrowallets/waas/app/services/cacheguard"
)

// ErrScanInProgress means another scanner holds the chain's scan lock.
var ErrScanInProgress = errors.New("deposit: another scan of this chain is running")

// scanLockTTL is how long a scanner that died keeps its chain locked. It is longer
// than the 60 s timeout of the deposit_scanner Lambda, so an invocation never loses
// the lock while it scans.
const scanLockTTL = 2 * time.Minute

func scanLockKey(chainID string) string {
	return "vault:lock:deposit_scan:" + chainID
}

// lockChain takes the chain's scan lock and returns its release. A lock another
// scanner holds is ErrScanInProgress; a cache that is missing or unreachable refuses
// too, so two scanners never share a chain while the lock cannot be checked.
func (s *Service) lockChain(chainID string) (func(), error) {
	if s.cache == nil {
		return nil, fmt.Errorf("deposit scan lock: cache is not configured")
	}
	key := scanLockKey(chainID)
	acquired, err := cacheguard.Acquire(s.cache, key, scanLockTTL)
	if err != nil {
		return nil, fmt.Errorf("deposit scan lock: %w", err)
	}
	if !acquired {
		return nil, ErrScanInProgress
	}
	return func() {
		if !s.cache.Forget(key) {
			slog.Warn("deposit scan lock not released; it expires on its own", "chain", chainID, "ttl", scanLockTTL.String())
		}
	}, nil
}

// holdChain takes the chain's scan lock like lockChain, and renews it until released
// (cacheguard.AcquireHold): a signature sync cycle that reads a long history keeps
// the chain for as long as it runs, while a holder that died frees it within
// scanLockTTL. It shares the key with lockChain, so it never runs beside a block scan
// of the chain.
func (s *Service) holdChain(chainID string) (func(), error) {
	if s.cache == nil {
		return nil, fmt.Errorf("deposit scan lock: cache is not configured")
	}
	hold, acquired, err := cacheguard.AcquireHold(s.cache, scanLockKey(chainID), scanLockTTL)
	if err != nil {
		return nil, fmt.Errorf("deposit scan lock: %w", err)
	}
	if !acquired {
		return nil, ErrScanInProgress
	}
	return hold.Release, nil
}
