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

// maxIssueDelay bounds how far ahead of this server's clock a watermark may
// be before AwaitIssuable gives up instead of stalling a request.
const maxIssueDelay = 2 * time.Second

// SessionRevoker ends every dashboard session of a user: access JWTs through
// the watermark SessionAuth checks, refresh tokens by revoking them, and
// pending 2FA challenges because they are compared to the same watermark.
type SessionRevoker struct {
	watermarks SessionWatermarkStore
	refresh    RefreshTokenRevoker
	now        func() time.Time
	sleep      func(time.Duration)
}

func NewSessionRevoker(watermarks SessionWatermarkStore, refresh RefreshTokenRevoker) (*SessionRevoker, error) {
	if watermarks == nil || refresh == nil {
		return nil, errors.New("auth: session revoker: all dependencies are required")
	}
	return &SessionRevoker{watermarks: watermarks, refresh: refresh, now: time.Now, sleep: time.Sleep}, nil
}

// WithClock replaces the time source and the sleeper; tests use it to pin
// the watermark and observe waits.
func (r *SessionRevoker) WithClock(now func() time.Time, sleep func(time.Duration)) *SessionRevoker {
	clone := *r
	clone.now = now
	clone.sleep = sleep
	return &clone
}

// RevokeAll revokes the refresh tokens and moves the watermark to the start
// of the next second. JWTs carry iat in whole seconds and Goravel's tokens
// hold nothing else that varies, so two tokens of one user minted in the same
// second are identical: the whole current second has to be void, and new
// sessions wait for the watermark (AwaitIssuable).
func (r *SessionRevoker) RevokeAll(userID uuid.UUID) (time.Time, error) {
	if userID == uuid.Nil {
		return time.Time{}, errors.New("auth: revoke sessions: user id is required")
	}
	watermark := r.now().UTC().Truncate(time.Second).Add(time.Second)
	if err := r.watermarks.UpdateSessionsRevokedAt(userID, watermark); err != nil {
		return time.Time{}, fmt.Errorf("auth: revoke sessions: watermark: %w", err)
	}
	if err := r.refresh.RevokeAllForUser(userID); err != nil {
		return time.Time{}, fmt.Errorf("auth: revoke sessions: refresh tokens: %w", err)
	}
	return watermark, nil
}

// AwaitIssuable blocks until a session minted now would postdate the user's
// watermark. Only a session issued within a second of a revocation waits.
func (r *SessionRevoker) AwaitIssuable(revokedAt *time.Time) error {
	if revokedAt == nil {
		return nil
	}
	wait := revokedAt.Sub(r.now())
	if wait <= 0 {
		return nil
	}
	if wait > maxIssueDelay {
		return fmt.Errorf("auth: sessions revoked until %s, %s ahead of this server's clock", revokedAt.UTC().Format(time.RFC3339), wait)
	}
	r.sleep(wait)
	return nil
}

// SessionRevoked reports whether something issued at issuedAt (a JWT iat or
// a challenge) predates the user's watermark.
func SessionRevoked(issuedAt time.Time, revokedAt *time.Time) bool {
	if revokedAt == nil {
		return false
	}
	return issuedAt.Before(*revokedAt)
}
