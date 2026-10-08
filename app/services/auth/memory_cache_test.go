package auth_test

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/goravel/framework/contracts/cache"
	"github.com/goravel/framework/contracts/testing/docker"
)

// memCache is the cache port CacheTOTPChallengeStore and CacheAttemptLimiter already take.
type memCache struct {
	mu      sync.Mutex
	entries map[string]memCacheEntry
}

type memCacheEntry struct {
	value   any
	expires time.Time
}

func newMemCache() *memCache {
	return &memCache{entries: map[string]memCacheEntry{}}
}

func (c *memCache) live(key string, now time.Time) (memCacheEntry, bool) {
	entry, ok := c.entries[key]
	if !ok {
		return memCacheEntry{}, false
	}
	if !entry.expires.IsZero() && !now.Before(entry.expires) {
		delete(c.entries, key)
		return memCacheEntry{}, false
	}
	return entry, true
}

func (c *memCache) Add(key string, value any, ttl time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.live(key, time.Now()); ok {
		return false
	}
	c.entries[key] = memCacheEntry{value: value, expires: time.Now().Add(ttl)}
	return true
}

func (c *memCache) Decrement(key string, value ...int64) (int64, error) {
	delta := int64(1)
	if len(value) > 0 {
		delta = value[0]
	}
	return c.addInt(key, -delta)
}

func (c *memCache) Docker() (docker.CacheDriver, error) {
	return nil, errors.New("memory cache has no docker")
}

func (c *memCache) Forever(key string, value any) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = memCacheEntry{value: value}
	return true
}

func (c *memCache) Forget(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.entries[key]
	delete(c.entries, key)
	return ok
}

func (c *memCache) Flush() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]memCacheEntry{}
	return true
}

func (c *memCache) Get(key string, def ...any) any {
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

func (c *memCache) GetBool(key string, def ...bool) bool {
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

func (c *memCache) GetInt(key string, def ...int) int {
	n, ok := asInt64(c.Get(key))
	if !ok {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	return int(n)
}

func (c *memCache) GetInt64(key string, def ...int64) int64 {
	n, ok := asInt64(c.Get(key))
	if !ok {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	return n
}

func (c *memCache) GetString(key string, def ...string) string {
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

func (c *memCache) Has(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.live(key, time.Now())
	return ok
}

func (c *memCache) Increment(key string, value ...int64) (int64, error) {
	delta := int64(1)
	if len(value) > 0 {
		delta = value[0]
	}
	return c.addInt(key, delta)
}

func (c *memCache) addInt(key string, delta int64) (int64, error) {
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
		c.entries[key] = memCacheEntry{value: next}
	}
	return next, nil
}

func (c *memCache) Lock(string, ...time.Duration) cache.Lock { return memLock{} }

func (c *memCache) Put(key string, value any, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = memCacheEntry{value: value, expires: time.Now().Add(ttl)}
	return nil
}

func (c *memCache) Pull(key string, def ...any) any {
	value := c.Get(key, def...)
	c.Forget(key)
	return value
}

func (c *memCache) Remember(key string, ttl time.Duration, callback func() (any, error)) (any, error) {
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

func (c *memCache) RememberForever(key string, callback func() (any, error)) (any, error) {
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

func (c *memCache) WithContext(context.Context) cache.Driver { return c }

func (c *memCache) keys() []string {
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

type memLock struct{}

func (memLock) Block(time.Duration, ...func()) bool                          { return false }
func (memLock) BlockWithTicker(time.Duration, time.Duration, ...func()) bool { return false }
func (memLock) Get(...func()) bool                                           { return false }
func (memLock) Release() bool                                                { return false }
func (memLock) ForceRelease() bool                                           { return false }

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
