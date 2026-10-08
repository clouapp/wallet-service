package sweep

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/tests/memcache"
)

// burst releases n goroutines together once every one is waiting.
func burst(n int, work func()) {
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	ready.Add(n)
	done.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			work()
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
}

func TestService_AcquireWalletOpsLock_ExactlyOneCallerWins(t *testing.T) {
	svc := &service{cache: memcache.New()}
	walletID := uuid.New()
	const callers = 16

	var wins, blocked atomic.Int32
	var releaseMu sync.Mutex
	var release func()

	burst(callers, func() {
		unlock, err := svc.acquireWalletOpsLock(walletID)
		if err == nil {
			wins.Add(1)
			releaseMu.Lock()
			release = unlock
			releaseMu.Unlock()
			return
		}
		if errors.Is(err, ErrInFlightConsolidation) {
			blocked.Add(1)
		}
	})

	require.Equal(t, int32(1), wins.Load())
	require.Equal(t, int32(callers-1), blocked.Load())
	require.NotNil(t, release)
	release()
}

func TestService_IncrDailyQuota_ExactlyOneCallerWins(t *testing.T) {
	svc := &service{cache: memcache.New()}
	accountID := uuid.New()
	limits := &Limits{MaxConsolidateReqPerDay: 1}
	const callers = 16

	var wins, blocked atomic.Int32
	burst(callers, func() {
		err := svc.incrDailyQuota(accountID, limits)
		if err == nil {
			wins.Add(1)
			return
		}
		if errors.Is(err, ErrDailyQuotaExceeded) {
			blocked.Add(1)
		}
	})

	require.Equal(t, int32(1), wins.Load())
	require.Equal(t, int32(callers-1), blocked.Load())
}
