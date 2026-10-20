package deposit

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/macrowallets/waas/pkg/deadline"
)

const (
	// healthReadBudget bounds one reading of the health check, whatever the store's
	// client does with its context's deadline.
	healthReadBudget = pendingHealthTimeout
	// healthWaitSlack is how much longer than the budget a check waits for the
	// reading in flight, so it gets that reading's outcome rather than the last one.
	healthWaitSlack = 250 * time.Millisecond
	// healthFailureBackoff is how long after a failed reading the checks are answered
	// from it without reading again: a store that is down costs one reading budget
	// per backoff, not one per check.
	healthFailureBackoff = 10 * time.Second
)

// healthSnapshot keeps the last reading of one part of the health check and the
// reading in flight. Its zero value is ready to use.
type healthSnapshot[T any] struct {
	mu    sync.Mutex
	at    time.Time
	have  bool
	value T
	// failed is set while the latest reading failed; failure is what the checks get
	// then, without reading again until failedAt + healthFailureBackoff.
	failed   bool
	failedAt time.Time
	failure  T
	// reading is closed when the reading in flight ends; nil when none is.
	reading chan struct{}
}

// healthRead describes one reading. Read takes it; Unavailable is what a check gets
// with no reading to serve; Failure is the message a failed reading logs; Busy is the
// error of a check that gave up waiting before any reading landed. KeepFailure serves
// a failed reading itself (its value carries the error) instead of the last good one.
type healthRead[T any] struct {
	Read        func(ctx context.Context) (T, error)
	Unavailable func(err error) T
	KeepFailure bool
	Failure     string
	Busy        error
}

// get returns the last reading while it is younger than ttl, and the failed reading's
// answer during healthFailureBackoff after a failure. Otherwise it starts a reading,
// unless one is in flight, and waits for it at most healthReadBudget (plus a little)
// or until ctx ends. The reading runs apart from the request: a check whose client
// left does not cancel the reading other checks wait on, and it lands in the
// snapshot for the next check. A reading is bounded by healthReadBudget even when the
// store's client ignores its context, and a panic in it is a failed reading.
func (c *healthSnapshot[T]) get(ctx context.Context, now time.Time, ttl time.Duration, read healthRead[T]) T {
	c.mu.Lock()
	if c.have && !c.failed && now.Sub(c.at) < ttl {
		defer c.mu.Unlock()
		return c.value
	}
	if c.failed && now.Sub(c.failedAt) < healthFailureBackoff {
		defer c.mu.Unlock()
		return c.failure
	}
	done := c.reading
	if done == nil {
		done = make(chan struct{})
		c.reading = done
		go c.refresh(context.WithoutCancel(ctx), now, read, done)
	}
	c.mu.Unlock()

	timer := time.NewTimer(healthReadBudget + healthWaitSlack)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	case <-ctx.Done():
	}
	return c.current(read)
}

// refresh takes one reading and stores it; the reading in flight is cleared whatever
// happens, so a panic cannot leave the checks waiting on it.
func (c *healthSnapshot[T]) refresh(ctx context.Context, now time.Time, read healthRead[T], done chan struct{}) {
	defer func() {
		c.mu.Lock()
		c.reading = nil
		c.mu.Unlock()
		close(done)
	}()
	readCtx, cancel := context.WithTimeout(ctx, healthReadBudget)
	defer cancel()
	value, err := deadline.Wait(healthReadBudget, func() (T, error) { return read.Read(readCtx) })
	c.store(now, read, value, err)
}

// store keeps a good reading, or the answer to give for a failed one: the reading
// itself with KeepFailure (a reading that ran out of time or panicked has none:
// unavailable), else the last good reading, else unavailable.
func (c *healthSnapshot[T]) store(now time.Time, read healthRead[T], value T, err error) {
	if err != nil {
		slog.Error(read.Failure, "error", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.at, c.have, c.value, c.failed = now, true, value, false
		return
	}
	answer := read.Unavailable(err)
	switch {
	case read.KeepFailure && !errors.Is(err, deadline.ErrExceeded) && !errors.Is(err, deadline.ErrPanicked):
		answer = value
	case !read.KeepFailure && c.have:
		answer = c.value
	}
	c.failed, c.failedAt, c.failure = true, now, answer
}

// current is the latest outcome: the failed reading's answer, the last good reading,
// or unavailable when none landed yet.
func (c *healthSnapshot[T]) current(read healthRead[T]) T {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.failed:
		return c.failure
	case c.have:
		return c.value
	default:
		return read.Unavailable(read.Busy)
	}
}
