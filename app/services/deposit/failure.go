package deposit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit/pending"
)

const (
	// A failed block is retried right away DefaultImmediateRetries times, waiting
	// 100 ms, 300 ms and 900 ms (each delay ImmediateRetryBackoffFactor times the last).
	DefaultImmediateRetries     = 3
	DefaultImmediateRetryDelay  = 100 * time.Millisecond
	ImmediateRetryBackoffFactor = 3
	MaxImmediateRetries         = 10

	// A pending block is retried after 30 s, doubling per failed retry up to 30 min.
	DefaultPendingRetryDelay    = 30 * time.Second
	DefaultPendingRetryMaxDelay = 30 * time.Minute

	// More failing blocks than this in one cycle look like an outage, not a bad block:
	// the cycle stops there and the checkpoint stays before the next failing block.
	DefaultMaxNewPendingPerCycle = 10
)

// FailurePolicy says how a block whose deposits could not be recorded is retried:
// first right away, then from the pending list with exponential backoff.
type FailurePolicy struct {
	ImmediateRetries      int
	ImmediateRetryDelay   time.Duration
	PendingRetryDelay     time.Duration
	PendingRetryMaxDelay  time.Duration
	MaxNewPendingPerCycle int
}

func DefaultFailurePolicy() FailurePolicy {
	return FailurePolicy{
		ImmediateRetries:      DefaultImmediateRetries,
		ImmediateRetryDelay:   DefaultImmediateRetryDelay,
		PendingRetryDelay:     DefaultPendingRetryDelay,
		PendingRetryMaxDelay:  DefaultPendingRetryMaxDelay,
		MaxNewPendingPerCycle: DefaultMaxNewPendingPerCycle,
	}
}

// FailurePolicyFromSettings builds a policy from configured values, where 0 keeps the
// default; negative values are rejected.
func FailurePolicyFromSettings(immediateRetries, immediateDelayMillis, pendingDelaySeconds, pendingMaxDelaySeconds, maxNewPendingPerCycle int) (FailurePolicy, error) {
	if immediateRetries < 0 || immediateDelayMillis < 0 || pendingDelaySeconds < 0 || pendingMaxDelaySeconds < 0 || maxNewPendingPerCycle < 0 {
		return FailurePolicy{}, fmt.Errorf("deposit failure settings must not be negative (retries %d, delay %d ms, pending delay %d s, pending max delay %d s, max new pending %d)",
			immediateRetries, immediateDelayMillis, pendingDelaySeconds, pendingMaxDelaySeconds, maxNewPendingPerCycle)
	}
	policy := DefaultFailurePolicy()
	if immediateRetries > 0 {
		policy.ImmediateRetries = immediateRetries
	}
	if immediateDelayMillis > 0 {
		policy.ImmediateRetryDelay = time.Duration(immediateDelayMillis) * time.Millisecond
	}
	if pendingDelaySeconds > 0 {
		policy.PendingRetryDelay = time.Duration(pendingDelaySeconds) * time.Second
	}
	if pendingMaxDelaySeconds > 0 {
		policy.PendingRetryMaxDelay = time.Duration(pendingMaxDelaySeconds) * time.Second
	} else {
		policy.PendingRetryMaxDelay = max(policy.PendingRetryMaxDelay, policy.PendingRetryDelay)
	}
	if maxNewPendingPerCycle > 0 {
		policy.MaxNewPendingPerCycle = maxNewPendingPerCycle
	}
	if err := policy.Validate(); err != nil {
		return FailurePolicy{}, err
	}
	return policy, nil
}

func (p FailurePolicy) Validate() error {
	switch {
	case p.ImmediateRetries < 0 || p.ImmediateRetries > MaxImmediateRetries:
		return fmt.Errorf("immediate retries must be between 0 and %d, got %d", MaxImmediateRetries, p.ImmediateRetries)
	case p.ImmediateRetries > 0 && p.ImmediateRetryDelay <= 0:
		return errors.New("immediate retry delay must be positive")
	case p.PendingRetryDelay <= 0:
		return errors.New("pending retry delay must be positive")
	case p.PendingRetryMaxDelay < p.PendingRetryDelay:
		return fmt.Errorf("pending retry max delay (%s) must not be below the pending retry delay (%s)", p.PendingRetryMaxDelay, p.PendingRetryDelay)
	case p.MaxNewPendingPerCycle < 1:
		return fmt.Errorf("max new pending blocks per cycle must be at least 1, got %d", p.MaxNewPendingPerCycle)
	}
	return nil
}

// immediateDelays are the waits before each immediate retry, e.g. 100/300/900 ms.
func (p FailurePolicy) immediateDelays() []time.Duration {
	delays := make([]time.Duration, p.ImmediateRetries)
	delay := p.ImmediateRetryDelay
	for i := range delays {
		delays[i] = delay
		delay *= ImmediateRetryBackoffFactor
	}
	return delays
}

// pendingBackoff is the wait before retrying a pending block that failed attempts times.
func (p FailurePolicy) pendingBackoff(attempts int) time.Duration {
	delay := p.PendingRetryDelay
	for i := 1; i < attempts && delay < p.PendingRetryMaxDelay; i++ {
		delay *= 2
	}
	return min(delay, p.PendingRetryMaxDelay)
}

// SetFailurePolicy replaces the retry policy; an invalid one is rejected and the
// current one kept.
func (s *Service) SetFailurePolicy(policy FailurePolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	s.failure = policy
	return nil
}

// SetPendingStore wires where blocks that keep failing are kept. Without it a block
// that fails every immediate retry stops the scan before it, as nothing may be skipped.
func (s *Service) SetPendingStore(store pending.Store) {
	s.pending = store
}

// classifiedError tags a failure with the pending entry error class.
type classifiedError struct {
	class string
	err   error
}

func (e *classifiedError) Error() string { return e.err.Error() }
func (e *classifiedError) Unwrap() error { return e.err }

func classify(class string, err error) error {
	if err == nil {
		return nil
	}
	return &classifiedError{class: class, err: err}
}

func errorClass(err error) string {
	switch {
	case errors.Is(err, chain.ErrRateLimited):
		return pending.ClassRateLimited
	case errors.Is(err, chain.ErrProviderUnavailable), errors.Is(err, chain.ErrProvider):
		return pending.ClassRPC
	}
	var classified *classifiedError
	if errors.As(err, &classified) {
		return classified.class
	}
	return pending.ClassUnknown
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
