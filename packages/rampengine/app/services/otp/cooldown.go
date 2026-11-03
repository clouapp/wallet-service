package otp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/identity"
)

// ErrResendTooSoon is what a refused send wraps; errors.As a *CooldownError for
// the wait.
var ErrResendTooSoon = errors.New("otp: resend too soon")

// CooldownError carries how long the caller must wait.
type CooldownError struct {
	RetryAfter time.Duration
}

func (e *CooldownError) Error() string {
	return fmt.Sprintf("otp: resend too soon, retry after %s", e.RetryAfter)
}

func (e *CooldownError) Is(target error) bool { return target == ErrResendTooSoon }

// RetryAfter extracts the wait from a refused send.
func RetryAfter(err error) (time.Duration, bool) {
	var ce *CooldownError
	if errors.As(err, &ce) {
		return ce.RetryAfter, true
	}
	return 0, false
}

// Seconds renders a duration for clients, rounded up: showing 0 while the
// server still refuses would invite a request that fails.
func Seconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int((d + time.Second - 1) / time.Second)
}

// CooldownStore records when the next send for a key is allowed.
type CooldownStore interface {
	// Claim starts a cooldown of d unless one is running; when one is, ok is
	// false and retryAfter is the time left (at least one second).
	Claim(key string, d time.Duration) (retryAfter time.Duration, ok bool, err error)
	// Restart starts a cooldown of d unconditionally.
	Restart(key string, d time.Duration) error
	// Release ends a cooldown early, for a claim whose send failed.
	Release(key string) error
}

var _ CooldownStore = (*CacheCooldownStore)(nil)

// CacheCooldownStore keeps cooldowns in the cache. Add is set-if-absent, which
// is what makes two concurrent resends unable to both win. The value is the
// expiry instant, so a refusal can say how long is left.
type CacheCooldownStore struct{}

func NewCacheCooldownStore() *CacheCooldownStore { return &CacheCooldownStore{} }

func (CacheCooldownStore) Claim(key string, d time.Duration) (time.Duration, bool, error) {
	if facades.Cache().Add(key, time.Now().Add(d).UnixMilli(), d) {
		return 0, true, nil
	}
	left := time.Until(time.UnixMilli(facades.Cache().GetInt64(key, 0)))
	if left < time.Second {
		left = time.Second
	}
	return left, false, nil
}

func (CacheCooldownStore) Restart(key string, d time.Duration) error {
	if err := facades.Cache().Put(key, time.Now().Add(d).UnixMilli(), d); err != nil {
		return fmt.Errorf("otp: restart cooldown: %w", err)
	}
	return nil
}

func (CacheCooldownStore) Release(key string) error {
	facades.Cache().Forget(key)
	return nil
}

// ForgetResendCooldown clears the resend gate for one address and purpose.
func ForgetResendCooldown(audience string, purpose identity.OTPPurpose, email string) {
	facades.Cache().Forget(cooldownKey(audience, purpose, email))
}

// cooldownKey scopes a cooldown to one audience, one purpose and one address.
// The address is hashed so inboxes do not appear in cache keys.
func cooldownKey(audience string, purpose identity.OTPPurpose, email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return "otp:cooldown:" + audience + ":" + string(purpose) + ":" + hex.EncodeToString(sum[:])
}
