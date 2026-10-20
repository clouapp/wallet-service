package deposit

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// healthSnapshot keeps the last reading of one part of the health check and the
// reading in flight. Its zero value is ready to use.
type healthSnapshot[T any] struct {
	mu    sync.Mutex
	at    time.Time
	have  bool
	value T
	// reading is closed when the reading in flight ends; nil when none is.
	reading chan struct{}
}

// healthRead describes one reading: Read takes it, Unavailable is what a check
// reports with no reading yet, and Failure is the message a failed reading logs.
type healthRead[T any] struct {
	Read        func(ctx context.Context) (T, error)
	Unavailable func(err error) T
	Failure     string
	Busy        error
}

// get returns the last reading while it is younger than ttl. Otherwise one reading
// runs at a time and the lock is never held during it: a check that finds a reading
// in flight waits for it at most pendingHealthTimeout (or until ctx ends), then
// serves the last reading. A reading that fails serves the last reading too (logged);
// with none yet, the failure is reported, never a failure of the check.
func (c *healthSnapshot[T]) get(ctx context.Context, now time.Time, ttl time.Duration, read healthRead[T]) T {
	c.mu.Lock()
	if c.have && now.Sub(c.at) < ttl {
		defer c.mu.Unlock()
		return c.value
	}
	if inFlight := c.reading; inFlight != nil {
		c.mu.Unlock()
		timer := time.NewTimer(pendingHealthTimeout)
		defer timer.Stop()
		select {
		case <-inFlight:
		case <-timer.C:
		case <-ctx.Done():
		}
		return c.last(read, read.Busy)
	}
	done := make(chan struct{})
	c.reading = done
	c.mu.Unlock()

	value, err := read.Read(ctx)

	c.mu.Lock()
	c.reading = nil
	close(done)
	if err == nil {
		c.at, c.have, c.value = now, true, value
	}
	c.mu.Unlock()
	if err != nil {
		slog.Error(read.Failure, "error", err)
		return c.last(read, err)
	}
	return value
}

// last is the last reading, or what read reports as unavailable with none yet.
func (c *healthSnapshot[T]) last(read healthRead[T], err error) T {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.have {
		return c.value
	}
	return read.Unavailable(err)
}
