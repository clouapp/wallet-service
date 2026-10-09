// Package cacheguard holds the two cache patterns whose Goravel driver calls
// hide a backend outage: Add reports false and Increment is the only error
// carrier. Both helpers surface the outage so a caller can refuse instead of
// treating "cache down" as "key held" or "count zero".
package cacheguard

import (
	"errors"
	"fmt"
	"time"

	contractscache "github.com/goravel/framework/contracts/cache"
)

// ErrUnavailable means the cache refused a write for a reason other than contention.
var ErrUnavailable = errors.New("cache unavailable")

// Acquire sets key for ttl when it is absent and reports whether this caller
// set it. Add returns false both when the key is held and when the backend
// failed, so a false result is checked against Has: a key that is not there
// cannot be held, and the caller gets ErrUnavailable. If the holder released
// between the two calls the caller gets ErrUnavailable too; both outcomes
// refuse, and a retry settles it.
func Acquire(c contractscache.Driver, key string, ttl time.Duration) (bool, error) {
	if c.Add(key, 1, ttl) {
		return true, nil
	}
	if c.Has(key) {
		return false, nil
	}
	return false, fmt.Errorf("acquire %s: %w", key, ErrUnavailable)
}

// Count adds delta to the counter at key and returns the new total. A counter
// created by this call lives for ttl, measured from that call (a fixed window;
// later calls do not extend it). The error comes from Increment, which is the
// only counter call that carries a backend failure.
func Count(c contractscache.Driver, key string, delta int64, ttl time.Duration) (int64, error) {
	if c.Add(key, delta, ttl) {
		return delta, nil
	}
	total, err := c.Increment(key, delta)
	if err != nil {
		return 0, fmt.Errorf("count %s: %w", key, err)
	}
	// The key lapsed between Add and Increment and INCRBY recreated it without
	// an expiry; give it one so a window cannot become permanent.
	if total == delta {
		if err := c.Put(key, total, ttl); err != nil {
			return 0, fmt.Errorf("count %s: %w", key, err)
		}
	}
	return total, nil
}

// Read returns the counter at key, 0 when it is absent. Get cannot tell a
// missing key from an unreachable backend, so the read is an Increment by 0,
// whose error reaches the caller. On an absent key that leaves a "0" with no
// expiry; Count gives such a key its window on the next increment.
func Read(c contractscache.Driver, key string) (int64, error) {
	total, err := c.Increment(key, 0)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", key, err)
	}
	return total, nil
}
