package sweep

import (
	"context"
	"sync"
	"time"
)

// redisStore is an in-memory RedisStore. Sweep tests talk to the port the
// service already takes and do not open Redis.
type redisStore struct {
	mu sync.Mutex
	nx map[string]string
	n  map[string]int64
}

func newRedisStore() *redisStore {
	return &redisStore{nx: map[string]string{}, n: map[string]int64{}}
}

func (s *redisStore) SetNX(_ context.Context, key, value string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.nx[key]; ok {
		return false, nil
	}
	s.nx[key] = value
	return true, nil
}

func (s *redisStore) Del(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.nx, key)
	delete(s.n, key)
	return nil
}

func (s *redisStore) Incr(_ context.Context, key string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n[key]++
	return s.n[key], nil
}

func (s *redisStore) Expire(context.Context, string, time.Duration) error { return nil }

func (s *redisStore) Int(key string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.n[key]
	return n, ok
}
