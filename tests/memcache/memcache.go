// Package memcache is an in-memory contractscache.Driver for tests. It follows Redis
// semantics where the services depend on them: Add is set-if-absent, Increment
// creates a key without an expiry, and an expired key is gone.
package memcache

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/goravel/framework/contracts/cache"
	"github.com/goravel/framework/contracts/testing/docker"
)

// Cache is the in-memory driver; use New.
type Cache struct {
	mu      sync.Mutex
	entries map[string]item
}

type item struct {
	value   any
	expires time.Time
}

func New() *Cache {
	return &Cache{entries: map[string]item{}}
}

func (c *Cache) live(key string, now time.Time) (item, bool) {
	entry, ok := c.entries[key]
	if !ok {
		return item{}, false
	}
	if !entry.expires.IsZero() && !now.Before(entry.expires) {
		delete(c.entries, key)
		return item{}, false
	}
	return entry, true
}

func (c *Cache) Add(key string, value any, ttl time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.live(key, time.Now()); ok {
		return false
	}
	c.entries[key] = item{value: value, expires: expiry(ttl)}
	return true
}

func (c *Cache) Decrement(key string, value ...int64) (int64, error) {
	delta := int64(1)
	if len(value) > 0 {
		delta = value[0]
	}
	return c.addInt(key, -delta)
}

func (c *Cache) Docker() (docker.CacheDriver, error) {
	return nil, errors.New("memory cache has no docker")
}

func (c *Cache) Forever(key string, value any) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = item{value: value}
	return true
}

func (c *Cache) Forget(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.entries[key]
	delete(c.entries, key)
	return ok
}

func (c *Cache) Flush() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]item{}
	return true
}

func (c *Cache) Get(key string, def ...any) any {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.live(key, time.Now())
	if !ok {
		if len(def) > 0 {
			return def[0]
		}
		return nil
	}
	return entry.value
}

func (c *Cache) GetBool(key string, def ...bool) bool {
	value := c.Get(key)
	flag, ok := value.(bool)
	if !ok {
		if len(def) > 0 {
			return def[0]
		}
		return false
	}
	return flag
}

func (c *Cache) GetInt(key string, def ...int) int {
	n, ok := asInt64(c.Get(key))
	if !ok {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	return int(n)
}

func (c *Cache) GetInt64(key string, def ...int64) int64 {
	n, ok := asInt64(c.Get(key))
	if !ok {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	return n
}

func (c *Cache) GetString(key string, def ...string) string {
	value := c.Get(key)
	text, ok := value.(string)
	if !ok || text == "" {
		if len(def) > 0 {
			return def[0]
		}
		return ""
	}
	return text
}

func (c *Cache) Has(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.live(key, time.Now())
	return ok
}

func (c *Cache) Increment(key string, value ...int64) (int64, error) {
	delta := int64(1)
	if len(value) > 0 {
		delta = value[0]
	}
	return c.addInt(key, delta)
}

func (c *Cache) addInt(key string, delta int64) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.live(key, time.Now())
	var current int64
	if ok {
		parsed, parsedOK := asInt64(entry.value)
		if !parsedOK {
			return 0, errors.New("cache value is not an integer")
		}
		current = parsed
	}
	next := current + delta
	if ok {
		entry.value = next
		c.entries[key] = entry
	} else {
		c.entries[key] = item{value: next}
	}
	return next, nil
}

func (c *Cache) Lock(string, ...time.Duration) cache.Lock { return lock{} }

func (c *Cache) Put(key string, value any, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = item{value: value, expires: expiry(ttl)}
	return nil
}

func (c *Cache) Pull(key string, def ...any) any {
	value := c.Get(key, def...)
	c.Forget(key)
	return value
}

func (c *Cache) Remember(key string, ttl time.Duration, callback func() (any, error)) (any, error) {
	if c.Has(key) {
		return c.Get(key), nil
	}
	value, err := callback()
	if err != nil {
		return nil, err
	}
	if err := c.Put(key, value, ttl); err != nil {
		return nil, err
	}
	return value, nil
}

func (c *Cache) RememberForever(key string, callback func() (any, error)) (any, error) {
	if c.Has(key) {
		return c.Get(key), nil
	}
	value, err := callback()
	if err != nil {
		return nil, err
	}
	c.Forever(key, value)
	return value, nil
}

func (c *Cache) WithContext(context.Context) cache.Driver { return c }

func (c *Cache) Keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	var out []string
	for key := range c.entries {
		if _, ok := c.live(key, now); ok {
			out = append(out, key)
		}
	}
	return out
}

type lock struct{}

func (lock) Block(time.Duration, ...func()) bool                          { return false }
func (lock) BlockWithTicker(time.Duration, time.Duration, ...func()) bool { return false }
func (lock) Get(...func()) bool                                           { return false }
func (lock) Release() bool                                                { return false }
func (lock) ForceRelease() bool                                           { return false }

func asInt64(value any) (int64, bool) {
	switch n := value.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

// ErrUnavailable is what Down's Increment and Put return.
var ErrUnavailable = errors.New("cache unavailable")

// Down is a driver whose backend is unreachable, shaped like goravel/redis when
// Redis is down: Add reports false, reads miss, Has reports false, and only the
// methods that return an error (Increment, Decrement, Put) surface it.
type Down struct{ *Cache }

func (Down) Add(string, any, time.Duration) bool        { return false }
func (Down) Has(string) bool                            { return false }
func (Down) Get(_ string, def ...any) any               { return firstOf(def) }
func (Down) Forget(string) bool                         { return false }
func (Down) Increment(string, ...int64) (int64, error)  { return 0, ErrUnavailable }
func (Down) Decrement(string, ...int64) (int64, error)  { return 0, ErrUnavailable }
func (Down) Put(string, any, time.Duration) error       { return ErrUnavailable }
func (d Down) WithContext(context.Context) cache.Driver { return d }

func firstOf(def []any) any {
	if len(def) == 0 {
		return nil
	}
	return def[0]
}

// expiry is the deadline of a key written with ttl; a ttl of 0 never expires, as in Redis.
func expiry(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
}
