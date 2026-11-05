// Package stepup keeps the settings step-up window: after a session proves a
// second factor, it may change settings for a fixed time. The window belongs
// to the session id (the sid claim), lives in the Cache and is never renewed.
package stepup

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Window is how long a granted step-up lasts, counted from the grant.
const Window = 15 * time.Minute

const keyPrefix = "step-up:"

// Store is the part of the Cache the service uses. A key that is absent (or the
// backend unreachable) reads as 0, which Status reports as no window.
type Store interface {
	Put(key string, value any, ttl time.Duration) error
	GetInt64(key string, def ...int64) int64
	Forget(key string) bool
}

// Deps is everything the service uses. Store is required; a nil Now uses time.Now.
type Deps struct {
	Store Store
	Now   func() time.Time
}

// Service grants and reads the step-up window of a session.
type Service struct {
	store Store
	now   func() time.Time
}

// NewService builds the service.
func NewService(deps Deps) (*Service, error) {
	if deps.Store == nil {
		return nil, errors.New("stepup: store is required")
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &Service{store: deps.Store, now: now}, nil
}

func key(sid uuid.UUID) string { return keyPrefix + sid.String() }

// Grant opens a new window of 15 minutes for the session and returns when it
// ends (whole seconds, UTC). A window already open is replaced.
func (s *Service) Grant(sid uuid.UUID) (time.Time, error) {
	if sid == uuid.Nil {
		return time.Time{}, errors.New("stepup: session id is required")
	}
	expiresAt := s.now().UTC().Add(Window).Truncate(time.Second)
	if err := s.store.Put(key(sid), expiresAt.Unix(), Window); err != nil {
		return time.Time{}, fmt.Errorf("stepup: grant: %w", err)
	}
	return expiresAt, nil
}

// Status returns when the session's window ends, or nil when it has none or it
// ended. It never extends the window.
func (s *Service) Status(sid uuid.UUID) (*time.Time, error) {
	if sid == uuid.Nil {
		return nil, nil
	}
	unix := s.store.GetInt64(key(sid))
	if unix <= 0 {
		return nil, nil
	}
	expiresAt := time.Unix(unix, 0).UTC()
	if !expiresAt.After(s.now()) {
		return nil, nil
	}
	return &expiresAt, nil
}

// Forget drops the windows of the given sessions.
func (s *Service) Forget(sids ...uuid.UUID) {
	for _, sid := range sids {
		s.store.Forget(key(sid))
	}
}
