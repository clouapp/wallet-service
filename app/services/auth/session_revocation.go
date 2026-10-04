package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	activitylog "github.com/macrowallets/waas/app/services/activity"
	audit "github.com/macrowallets/waas/packages/activitylog"
)

// SessionWatermarkStore persists the instant before which a user's sessions
// are void. The context joins the caller's transaction.
type SessionWatermarkStore interface {
	UpdateSessionsRevokedAt(ctx context.Context, id uuid.UUID, at time.Time) error
}

// RefreshTokenRevoker voids every refresh token a user holds. The context
// joins the caller's transaction.
type RefreshTokenRevoker interface {
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
}

// SessionActivity appends one platform row on the caller's transaction.
type SessionActivity interface {
	Within(ctx context.Context, fn func(context.Context) error) error
	Append(ctx context.Context, row models.AccountActivity) error
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
	activity   SessionActivity
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
	if r == nil {
		return nil
	}
	clone := *r
	clone.now = now
	clone.sleep = sleep
	return &clone
}

// WithActivity attaches the platform activity writer. A nil writer leaves
// RevokeAll as a watermark and refresh-token revoke with no audit row.
func (r *SessionRevoker) WithActivity(activity SessionActivity) *SessionRevoker {
	if r == nil {
		return nil
	}
	clone := *r
	clone.activity = activity
	return &clone
}

// RevokeAll revokes the refresh tokens and moves the watermark to the start
// of the next second. JWTs carry iat in whole seconds and Goravel's tokens
// hold nothing else that varies, so two tokens of one user minted in the same
// second are identical: the whole current second has to be void, and new
// sessions wait for the watermark (AwaitIssuable). When an activity writer is
// attached, the watermark, the refresh-token revoke and the platform
// user.sessions_revoked row commit together.
func (r *SessionRevoker) RevokeAll(ctx context.Context, userID uuid.UUID) (time.Time, error) {
	return r.RevokeAllBy(ctx, userID, userID)
}

// RevokeAllBy is RevokeAll with a distinct actor. A platform admin revoking
// another user is the actor; the target is the user whose sessions end.
// The activity row stays a platform row (null account id).
func (r *SessionRevoker) RevokeAllBy(ctx context.Context, actorID, userID uuid.UUID) (time.Time, error) {
	if ctx == nil {
		return time.Time{}, errors.New("auth: revoke sessions: context is required")
	}
	if actorID == uuid.Nil {
		return time.Time{}, errors.New("auth: revoke sessions: actor is required")
	}
	if userID == uuid.Nil {
		return time.Time{}, errors.New("auth: revoke sessions: user id is required")
	}
	watermark := r.now().UTC().Truncate(time.Second).Add(time.Second)
	write := func(ctx context.Context) error {
		named := ctx
		if r.activity != nil {
			named = audit.WithIntent(ctx, audit.Intent{Event: activitylog.ActionUserSessionsRevoked})
		}
		if err := r.watermarks.UpdateSessionsRevokedAt(named, userID, watermark); err != nil {
			return fmt.Errorf("auth: revoke sessions: watermark: %w", err)
		}
		if err := r.refresh.RevokeAllForUser(ctx, userID); err != nil {
			return fmt.Errorf("auth: revoke sessions: refresh tokens: %w", err)
		}
		if r.activity == nil {
			return nil
		}
		meta, err := activitylog.SessionsRevoked()
		if err != nil {
			return err
		}
		return r.activity.Append(ctx, models.AccountActivity{
			ActorUserID: actorID,
			Action:      activitylog.ActionUserSessionsRevoked,
			TargetType:  activitylog.TargetUser,
			TargetID:    userID.String(),
			Metadata:    meta,
		})
	}
	if r.activity == nil {
		if err := write(ctx); err != nil {
			return time.Time{}, err
		}
		return watermark, nil
	}
	if err := r.activity.Within(ctx, write); err != nil {
		return time.Time{}, err
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
