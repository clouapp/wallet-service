// Package race makes a goroutine race test deterministic. It is its own package
// so a unit test can import it without the app that tests/support boots.
package race

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	testifymock "github.com/stretchr/testify/mock"

	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

// Race holds the first write of a contended row until the other caller writes
// too (nothing serialized them) or queues behind Locker, so a race test turns
// on the code under test rather than on the scheduler.
type Race struct {
	writes atomic.Int32
	met    chan struct{}
}

func New() *Race { return &Race{met: make(chan struct{}, 1)} }

// Write is called by a fake repository before it stores the contended row.
func (r *Race) Write() {
	if r.writes.Add(1) == 1 {
		<-r.met
		return
	}
	r.meet()
}

func (r *Race) meet() {
	select {
	case r.met <- struct{}{}:
	default:
	}
}

// Locker has the advisory lock's semantics in process; serialize=false is the
// negative control, a lock that admits every caller at once.
func (r *Race) Locker(t *testing.T, serialize bool) *mocks.MockLocker {
	var mu sync.Mutex
	locker := mocks.NewMockLocker(t)
	locker.EXPECT().WithLock(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(ctx context.Context, _ string, fn func(context.Context) error) error {
			if serialize {
				if !mu.TryLock() {
					r.meet()
					mu.Lock()
				}
				defer mu.Unlock()
			}
			return fn(ctx)
		}).Maybe()
	return locker
}

// Run starts n calls at once and returns their errors once all have finished.
func (r *Race) Run(n int, call func() error) []error {
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = call()
		}()
	}
	wg.Wait()
	return errs
}
