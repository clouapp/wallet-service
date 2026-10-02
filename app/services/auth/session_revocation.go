package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SessionWatermarkStore persists the instant before which a user's sessions
// are void.
type SessionWatermarkStore interface {
	UpdateSessionsRevokedAt(id uuid.UUID, at time.Time) error
}

// RefreshTokenRevoker voids every refresh token a user holds.
type RefreshTokenRevoker interface {
	RevokeAllForUser(userID uuid.UUID) error
}

// SessionRevoker ends every dashboard session of a user: access JWTs through
// the watermark SessionAuth checks, refresh tokens by revoking them, and
// pending 2FA challenges because they are compared to the same watermark.
type SessionRevoker struct {
	watermarks SessionWatermarkStore
	refresh    RefreshTokenRevoker
	now        func() time.Time
}

func NewSessionRevoker(watermarks SessionWatermarkStore, refresh RefreshTokenRevoker) (*SessionRevoker, error) {
	if watermarks == nil || refresh == nil {
		return nil, errors.New("auth: session revoker: all dependencies are required")
	}
	return &SessionRevoker{watermarks: watermarks, refresh: refresh, now: time.Now}, nil
}

// WithClock replaces the time source; tests use it to pin the watermark.
func (r *SessionRevoker) WithClock(now func() time.Time) *SessionRevoker {
	clone := *r
	clone.now = now
	return &clone
}

// RevokeAll moves the watermark to now and revokes the refresh tokens. The
// watermark is truncated to the second because JWT iat is: a session issued
// right after the revocation (the caller's replacement session) must survive.
func (r *SessionRevoker) RevokeAll(userID uuid.UUID) (time.Time, error) {
	if userID == uuid.Nil {
		return time.Time{}, errors.New("auth: revoke sessions: user id is required")
	}
	watermark := r.now().UTC().Truncate(time.Second)
	if err := r.watermarks.UpdateSessionsRevokedAt(userID, watermark); err != nil {
		return time.Time{}, fmt.Errorf("auth: revoke sessions: watermark: %w", err)
	}
	if err := r.refresh.RevokeAllForUser(userID); err != nil {
		return time.Time{}, fmt.Errorf("auth: revoke sessions: refresh tokens: %w", err)
	}
	return watermark, nil
}

// SessionRevoked reports whether something issued at issuedAt (a JWT iat or
// a challenge) predates the user's watermark.
func SessionRevoked(issuedAt time.Time, revokedAt *time.Time) bool {
	if revokedAt == nil {
		return false
	}
	return issuedAt.Before(*revokedAt)
}
