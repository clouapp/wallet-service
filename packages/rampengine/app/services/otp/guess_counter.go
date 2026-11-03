package otp

import (
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/identity"
)

// GuessCounter counts, per subject and purpose, the guesses at the outstanding
// code and the codes issued (each brings fresh guesses), with atomic INCRs so
// concurrent requests cannot lose counts or share one.
type GuessCounter interface {
	// Reset starts a fresh count for a code about to be issued, kept for ttl:
	// that code's lifetime.
	Reset(subject identity.Subject, purpose identity.OTPPurpose, ttl time.Duration) error
	// Claim counts one guess, right or wrong, before it is compared, and
	// returns the count so far: each concurrent guess gets its own.
	Claim(subject identity.Subject, purpose identity.OTPPurpose) (int64, error)
	// Issued counts one more code of the purpose and returns how many were
	// counted in the current IssueWindow, this one included.
	Issued(subject identity.Subject, purpose identity.OTPPurpose) (int64, error)
}

// RedisGuessCounter is the cache-backed (Redis) implementation.
type RedisGuessCounter struct{}

// NewRedisGuessCounter builds the default Redis-backed counter.
func NewRedisGuessCounter() *RedisGuessCounter {
	return &RedisGuessCounter{}
}

// counterKey is "<guard>:<purpose>:<counter>:<id>". The guard keeps admin 7
// and user 7 apart. The purpose keeps each count to its own code: shared,
// requesting a passwordless code, which takes nothing but an address, would
// reset the count on a reset code. The guess count's shape is the one the admin
// and client counters used before they were merged here, so a live code keeps
// its count across the deploy.
func counterKey(subject identity.Subject, purpose identity.OTPPurpose, counter string) (string, error) {
	guard := identity.GuardFor(subject)
	if guard == "" {
		return "", fmt.Errorf("otp: no guard for subject type %q", subject.SubjectType())
	}
	return fmt.Sprintf("%s:%s:%s:%d", guard, purpose, counter, subject.SubjectID()), nil
}

func (c *RedisGuessCounter) Reset(subject identity.Subject, purpose identity.OTPPurpose, ttl time.Duration) error {
	key, err := counterKey(subject, purpose, "verify_attempts")
	if err != nil {
		return err
	}
	if err := facades.Cache().Put(key, int64(0), ttl); err != nil {
		return fmt.Errorf("otp: reset guess count: %w", err)
	}
	return nil
}

func (c *RedisGuessCounter) Claim(subject identity.Subject, purpose identity.OTPPurpose) (int64, error) {
	key, err := counterKey(subject, purpose, "verify_attempts")
	if err != nil {
		return 0, err
	}
	n, err := facades.Cache().Increment(key)
	if err != nil {
		return 0, fmt.Errorf("otp: count guess: %w", err)
	}
	return n, nil
}

// Issued keys a fixed window, "<guard>:<purpose>:issued:<id>:<window>", so a
// count whose expiry was lost (an INCR landing just after it lapsed creates the
// key without one) is abandoned with its window instead of refusing forever.
func (c *RedisGuessCounter) Issued(subject identity.Subject, purpose identity.OTPPurpose) (int64, error) {
	key, err := counterKey(subject, purpose, "issued")
	if err != nil {
		return 0, err
	}
	key = fmt.Sprintf("%s:%d", key, time.Now().Unix()/int64(IssueWindow/time.Second))
	facades.Cache().Add(key, int64(0), IssueWindow)
	n, err := facades.Cache().Increment(key)
	if err != nil {
		return 0, fmt.Errorf("otp: count issued code: %w", err)
	}
	return n, nil
}
