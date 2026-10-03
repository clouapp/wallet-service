package users

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	activitylog "github.com/macrowallets/waas/app/services/activity"
	audit "github.com/macrowallets/waas/packages/activitylog"
)

// Suspension is the platform suspension of one user. SuspendedAt is nil when
// the user is not suspended.
type Suspension struct {
	ID          uuid.UUID
	SuspendedAt *time.Time
}

// PlatformAdmins reports whether a dashboard user may suspend platform users.
type PlatformAdmins interface {
	Contains(ctx context.Context, userID uuid.UUID) (bool, error)
}

// Sessions ends the dashboard sessions of a user. Suspension calls it so a
// watermark and the refresh tokens move with suspended_at.
type Sessions interface {
	RevokeAll(ctx context.Context, userID uuid.UUID) (time.Time, error)
}

// WithPlatformAdmins attaches the platform-admin lookup Suspend and
// Reactivate use.
func (s *Service) WithPlatformAdmins(admins PlatformAdmins) *Service {
	if s == nil {
		return nil
	}
	s.admins = admins
	return s
}

// WithSessions attaches the session revoker Suspend uses.
func (s *Service) WithSessions(sessions Sessions) *Service {
	if s == nil {
		return nil
	}
	s.sessions = sessions
	return s
}

// WithClock pins the suspension instant. Tests use it.
func (s *Service) WithClock(now func() time.Time) *Service {
	if s == nil {
		return nil
	}
	s.clock = now
	return s
}

// Suspend sets users.suspended_at, revokes the user's sessions, and writes
// user.suspended with a null account id. A platform admin is the only caller.
// Suspending a user who is already suspended does not write a second row.
func (s *Service) Suspend(ctx context.Context, actorID, targetID uuid.UUID) (Suspension, error) {
	return s.changeSuspension(ctx, actorID, targetID, true)
}

// Reactivate clears users.suspended_at and writes user.reactivated with a
// null account id. It does not restore sessions revoked by the suspension.
// A user who is not suspended does not get a second row.
func (s *Service) Reactivate(ctx context.Context, actorID, targetID uuid.UUID) (Suspension, error) {
	return s.changeSuspension(ctx, actorID, targetID, false)
}

func (s *Service) changeSuspension(ctx context.Context, actorID, targetID uuid.UUID, suspend bool) (Suspension, error) {
	op := "reactivate user"
	if suspend {
		op = "suspend user"
	}
	if ctx == nil {
		return Suspension{}, fmt.Errorf("%s: context is required", op)
	}
	if actorID == uuid.Nil {
		return Suspension{}, fmt.Errorf("%s: actor is required", op)
	}
	if targetID == uuid.Nil {
		return Suspension{}, fmt.Errorf("%s: user id is required", op)
	}
	if s == nil || s.store == nil {
		return Suspension{}, fmt.Errorf("users service: users repository is required")
	}
	if s.admins == nil {
		return Suspension{}, fmt.Errorf("%s: platform admins are required", op)
	}
	if s.activity == nil {
		return Suspension{}, fmt.Errorf("%s: activity is required", op)
	}
	if suspend && s.sessions == nil {
		return Suspension{}, fmt.Errorf("%s: sessions are required", op)
	}
	admin, err := s.admins.Contains(ctx, actorID)
	if err != nil {
		return Suspension{}, err
	}
	if !admin {
		return Suspension{}, ErrPlatformForbidden
	}

	var result Suspension
	err = s.activity.Within(ctx, func(ctx context.Context) error {
		user, err := s.store.FindByID(ctx, targetID)
		if err != nil {
			if errors.Is(err, models.ErrRepositoryNotFound) {
				return ErrNotFound
			}
			return err
		}
		if user == nil || user.ID == uuid.Nil {
			return ErrNotFound
		}
		already := policies.UserIsSuspended(user.SuspendedAt)
		if suspend == already {
			result = Suspension{ID: user.ID, SuspendedAt: user.SuspendedAt}
			return nil
		}
		at, action, meta, err := suspensionChange(suspend, s.now())
		if err != nil {
			return err
		}
		named := audit.WithIntent(ctx, audit.Intent{Event: action})
		if err := s.store.SetSuspendedAt(named, targetID, at); err != nil {
			return err
		}
		if suspend {
			if _, err := s.sessions.RevokeAll(ctx, targetID); err != nil {
				return err
			}
		}
		if err := s.activity.Append(ctx, models.AccountActivity{
			ActorUserID: actorID,
			Action:      action,
			TargetType:  activitylog.TargetUser,
			TargetID:    targetID.String(),
			Metadata:    meta,
		}); err != nil {
			return err
		}
		result = Suspension{ID: targetID, SuspendedAt: at}
		return nil
	})
	if err != nil {
		return Suspension{}, err
	}
	return result, nil
}

func suspensionChange(suspend bool, now time.Time) (*time.Time, string, models.ActivityMetadata, error) {
	if suspend {
		stamped := now.UTC()
		meta, err := activitylog.UserSuspended()
		if err != nil {
			return nil, "", nil, err
		}
		return &stamped, activitylog.ActionUserSuspended, meta, nil
	}
	meta, err := activitylog.UserReactivated()
	if err != nil {
		return nil, "", nil, err
	}
	return nil, activitylog.ActionUserReactivated, meta, nil
}

func (s *Service) now() time.Time {
	if s != nil && s.clock != nil {
		return s.clock()
	}
	return time.Now()
}
