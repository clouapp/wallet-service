package mfa

import (
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/identity"
)

// StepUpCounter counts step-up attempts per subject in a window. An attempt is
// claimed before its code is checked (see Service.ValidateStepUpCode), so the
// counter is the enforcement, and it has no read that does not claim: a
// read-then-check would let concurrent requests all see the same count and all
// earn a guess.
type StepUpCounter interface {
	// Claim takes one attempt and returns the count so far. The first claim
	// opens the window, which lasts window from then.
	Claim(subject identity.Subject, window time.Duration) (int64, error)
	// Clear closes the window once a code checks out.
	Clear(subject identity.Subject) error
}

// RedisStepUpCounter is the cache-backed (Redis) implementation, shared by
// both guards.
type RedisStepUpCounter struct{}

// NewRedisStepUpCounter builds the default Redis-backed counter.
func NewRedisStepUpCounter() *RedisStepUpCounter {
	return &RedisStepUpCounter{}
}

// stepUpKey is "<guard>:step_up:verify_attempts:<id>". The guard keeps admin 7
// and user 7 apart; the admin shape is the one the admin counter used before it
// moved here, so a window open across the deploy stays open.
func stepUpKey(subject identity.Subject) (string, error) {
	guard := identity.GuardFor(subject)
	if guard == "" {
		return "", fmt.Errorf("mfa: no guard for subject type %q", subject.SubjectType())
	}
	return fmt.Sprintf("%s:step_up:verify_attempts:%d", guard, subject.SubjectID()), nil
}

// Claim opens the window with its TTL on the first attempt and only increments
// after, so the window runs from the first attempt and cannot be held open by
// guessing on. A missing key is zero attempts, which is also what a lapsed
// window looks like: its expiry is what lets a locked-out subject try again
// without anyone intervening.
//
// The window can lapse between the Add that found it open and the Increment.
// INCR then recreates the key at 1 without a TTL, a counter that would never
// lapse and, past the cap, would refuse the right code for good. A count of 1
// after a failed Add is exactly that case, and this claim is the first of a
// new window, so it puts the key back with the window's TTL. Between the
// INCR and that Put the key has no expiry for one round trip. A concurrent
// claim landing there keeps the count it got, but the Put sets the stored
// count back to 1 under it, so that one new window can end up as many
// attempts short as such claims.
func (c *RedisStepUpCounter) Claim(subject identity.Subject, window time.Duration) (int64, error) {
	key, err := stepUpKey(subject)
	if err != nil {
		return 0, err
	}
	if facades.Cache().Add(key, int64(1), window) {
		return 1, nil
	}
	n, err := facades.Cache().Increment(key)
	if err != nil {
		return 0, fmt.Errorf("mfa: claim step-up attempt: %w", err)
	}
	if n == 1 {
		if err := facades.Cache().Put(key, n, window); err != nil {
			return 0, fmt.Errorf("mfa: reopen the step-up window: %w", err)
		}
	}
	return n, nil
}

func (c *RedisStepUpCounter) Clear(subject identity.Subject) error {
	key, err := stepUpKey(subject)
	if err != nil {
		return err
	}
	facades.Cache().Forget(key)
	return nil
}
