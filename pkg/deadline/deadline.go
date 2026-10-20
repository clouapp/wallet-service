// Package deadline waits for a call at most a budget, also when the call ignores
// its context's deadline: a go-redis client built without ContextTimeoutEnabled
// (as the framework builds it) waits for its socket timeout instead, so a store that
// accepts the connection and never answers would hold the caller that long.
package deadline

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrExceeded is a call still running when its budget ran out.
	ErrExceeded = errors.New("deadline: budget exceeded")
	// ErrPanicked is a call that panicked.
	ErrPanicked = errors.New("deadline: call panicked")
)

// Wait runs call in its own goroutine and returns its result, or ErrExceeded once
// budget has passed; the call then finishes on its own and its result is dropped,
// so call must bound itself (its context) and touch no state the caller reads. A
// panic in call is returned as an error.
func Wait[T any](budget time.Duration, call func() (T, error)) (T, error) {
	type result struct {
		value T
		err   error
	}
	results := make(chan result, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				results <- result{err: fmt.Errorf("%w: %v", ErrPanicked, recovered)}
			}
		}()
		value, err := call()
		results <- result{value: value, err: err}
	}()
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case got := <-results:
		return got.value, got.err
	case <-timer.C:
		var zero T
		return zero, ErrExceeded
	}
}
