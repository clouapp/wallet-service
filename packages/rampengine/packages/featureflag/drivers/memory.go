package drivers

import (
	"context"
	"sync"

	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
)

// Memory is an in-process driver backed by a map guarded by an RWMutex.
// Values are JSON-encoded so its behavior matches the Redis store exactly.
type Memory struct {
	mu     sync.RWMutex
	values map[string]map[string][]byte // feature -> scopeKey -> JSON
}

// NewMemory returns an empty Memory.
func NewMemory() *Memory {
	return &Memory{values: make(map[string]map[string][]byte)}
}

func (s *Memory) Get(_ context.Context, feature, scopeKey string) (any, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	scopes, ok := s.values[feature]
	if !ok {
		return nil, false, nil
	}
	raw, ok := scopes[scopeKey]
	if !ok {
		return nil, false, nil
	}
	v, err := decodeValue(raw)
	if err != nil {
		return nil, false, err
	}
	return v, true, nil
}

func (s *Memory) Set(_ context.Context, feature, scopeKey string, value any) error {
	raw, err := encodeValue(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putLocked(feature, scopeKey, raw)
	return nil
}

func (s *Memory) SetIfAbsent(_ context.Context, feature, scopeKey string, value any) (bool, error) {
	raw, err := encodeValue(value)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if scopes, ok := s.values[feature]; ok {
		if _, exists := scopes[scopeKey]; exists {
			return false, nil
		}
	}
	s.putLocked(feature, scopeKey, raw)
	return true, nil
}

func (s *Memory) SetForAllScopes(_ context.Context, feature string, value any) error {
	raw, err := encodeValue(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	scopes, ok := s.values[feature]
	if !ok {
		return nil
	}
	for scopeKey := range scopes {
		scopes[scopeKey] = raw
	}
	return nil
}

func (s *Memory) Delete(_ context.Context, feature, scopeKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	scopes, ok := s.values[feature]
	if !ok {
		return nil
	}
	delete(scopes, scopeKey)
	if len(scopes) == 0 {
		delete(s.values, feature)
	}
	return nil
}

func (s *Memory) Purge(_ context.Context, features []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(features) == 0 {
		s.values = make(map[string]map[string][]byte)
		return nil
	}
	for _, f := range features {
		delete(s.values, f)
	}
	return nil
}

// putLocked writes raw under (feature, scopeKey). Caller must hold s.mu.
func (s *Memory) putLocked(feature, scopeKey string, raw []byte) {
	scopes, ok := s.values[feature]
	if !ok {
		scopes = make(map[string][]byte)
		s.values[feature] = scopes
	}
	scopes[scopeKey] = raw
}

var _ contracts.Driver = (*Memory)(nil)
