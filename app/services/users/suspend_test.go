package users_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	activitylog "github.com/macrowallets/waas/app/services/activity"
	"github.com/macrowallets/waas/app/services/users"
)

func TestSuspendWritesOnePlatformRowAndReactivateClearsIt(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	target := uuid.New()
	store := &suspensionStore{user: &models.User{ID: target, Status: models.StatusActive}}
	activity := &suspensionActivity{}
	sessions := &suspensionSessions{}
	when := time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)
	service := users.NewService(store).
		WithActivity(activity).
		WithPlatformAdmins(allowAdmins{actor}).
		WithSessions(sessions).
		WithClock(func() time.Time { return when })

	suspended, err := service.Suspend(context.Background(), actor, target)
	require.NoError(t, err)
	require.Equal(t, target, suspended.ID)
	require.NotNil(t, suspended.SuspendedAt)
	require.True(t, when.Equal(*suspended.SuspendedAt))
	require.Equal(t, []uuid.UUID{target}, sessions.revoked)
	require.Len(t, activity.rows, 1)
	require.Nil(t, activity.rows[0].AccountID)
	require.Equal(t, activitylog.ActionUserSuspended, activity.rows[0].Action)
	require.Equal(t, actor, activity.rows[0].ActorUserID)
	require.NotEqual(t, activitylog.ActionMemberSuspended, activity.rows[0].Action)
	encoded, err := activity.rows[0].Metadata.Encode()
	require.NoError(t, err)
	require.NotContains(t, encoded, "password")
	require.NotContains(t, encoded, "secret")

	again, err := service.Suspend(context.Background(), actor, target)
	require.NoError(t, err)
	require.True(t, when.Equal(*again.SuspendedAt))
	require.Len(t, activity.rows, 1)
	require.Len(t, sessions.revoked, 1)

	cleared, err := service.Reactivate(context.Background(), actor, target)
	require.NoError(t, err)
	require.Nil(t, cleared.SuspendedAt)
	require.Nil(t, store.user.SuspendedAt)
	require.Equal(t, activitylog.ActionUserReactivated, activity.rows[1].Action)
	require.Nil(t, activity.rows[1].AccountID)

	_, err = service.Reactivate(context.Background(), actor, target)
	require.NoError(t, err)
	require.Len(t, activity.rows, 2)
}

func TestSuspendRefusesACallerWhoIsNotAPlatformAdmin(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	target := uuid.New()
	store := &suspensionStore{user: &models.User{ID: target, Status: models.StatusActive}}
	activity := &suspensionActivity{}
	service := users.NewService(store).
		WithActivity(activity).
		WithPlatformAdmins(allowAdmins{}).
		WithSessions(&suspensionSessions{})

	_, err := service.Suspend(context.Background(), actor, target)
	require.ErrorIs(t, err, users.ErrPlatformForbidden)
	require.Nil(t, store.user.SuspendedAt)
	require.Empty(t, activity.rows)

	_, err = service.Reactivate(context.Background(), actor, target)
	require.ErrorIs(t, err, users.ErrPlatformForbidden)
}

func TestRevokeSessionsRecordsTheAdminAndRefusesEveryoneElse(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	target := uuid.New()
	store := &suspensionStore{user: &models.User{ID: target, Status: models.StatusActive}}
	activity := &suspensionActivity{}
	sessions := &suspensionSessions{}
	service := users.NewService(store).
		WithActivity(activity).
		WithPlatformAdmins(allowAdmins{actor}).
		WithSessions(sessions)

	require.NoError(t, service.RevokeSessions(context.Background(), actor, target))
	require.Equal(t, []uuid.UUID{actor}, sessions.actors)
	require.Equal(t, []uuid.UUID{target}, sessions.targets)
	require.Empty(t, activity.rows)
	require.Nil(t, store.user.SuspendedAt)
	require.Empty(t, sessions.revoked)

	require.NoError(t, service.RevokeSessions(context.Background(), actor, target))
	require.Len(t, sessions.targets, 2)

	err := users.NewService(store).
		WithActivity(activity).
		WithPlatformAdmins(allowAdmins{}).
		WithSessions(sessions).
		RevokeSessions(context.Background(), uuid.New(), target)
	require.ErrorIs(t, err, users.ErrSessionsForbidden)
	require.Len(t, sessions.targets, 2)

	missing := users.NewService(&suspensionStore{err: models.ErrRepositoryNotFound}).
		WithActivity(activity).
		WithPlatformAdmins(allowAdmins{actor}).
		WithSessions(sessions)
	require.ErrorIs(t, missing.RevokeSessions(context.Background(), actor, uuid.New()), users.ErrNotFound)
}

func TestSuspendReportsAMissingUser(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	store := &suspensionStore{err: models.ErrRepositoryNotFound}
	service := users.NewService(store).
		WithActivity(&suspensionActivity{}).
		WithPlatformAdmins(allowAdmins{actor}).
		WithSessions(&suspensionSessions{})

	_, err := service.Suspend(context.Background(), actor, uuid.New())
	require.ErrorIs(t, err, users.ErrNotFound)
}

type allowAdmins []uuid.UUID

func (a allowAdmins) Contains(_ context.Context, userID uuid.UUID) (bool, error) {
	for _, id := range a {
		if id == userID {
			return true, nil
		}
	}
	return false, nil
}

type suspensionStore struct {
	user *models.User
	err  error
}

func (s *suspensionStore) FindByEmail(context.Context, string) (*models.User, error) {
	return nil, errors.New("unused")
}
func (s *suspensionStore) FindByID(context.Context, uuid.UUID) (*models.User, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.user, nil
}
func (s *suspensionStore) Create(context.Context, *models.User) error { return errors.New("unused") }
func (s *suspensionStore) UpdateDefaultAccountID(context.Context, uuid.UUID, *uuid.UUID) error {
	return errors.New("unused")
}
func (s *suspensionStore) UpdateFullName(context.Context, uuid.UUID, string) error {
	return errors.New("unused")
}
func (s *suspensionStore) UpdatePasswordHash(context.Context, uuid.UUID, string) error {
	return errors.New("unused")
}
func (s *suspensionStore) UpdatePreferences(context.Context, uuid.UUID, *models.UserPreferences) error {
	return errors.New("unused")
}
func (s *suspensionStore) UpdateTotpSecret(context.Context, uuid.UUID, string) error {
	return errors.New("unused")
}
func (s *suspensionStore) EnableTotp(context.Context, uuid.UUID) error  { return errors.New("unused") }
func (s *suspensionStore) DisableTotp(context.Context, uuid.UUID) error { return errors.New("unused") }
func (s *suspensionStore) SetSuspendedAt(_ context.Context, id uuid.UUID, at *time.Time) error {
	if s.user == nil || s.user.ID != id {
		return errors.New("missing user")
	}
	s.user.SuspendedAt = at
	return nil
}

type suspensionActivity struct {
	rows []models.AccountActivity
}

func (a *suspensionActivity) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func (a *suspensionActivity) Append(_ context.Context, row models.AccountActivity) error {
	a.rows = append(a.rows, row)
	return nil
}

type suspensionSessions struct {
	revoked []uuid.UUID
	actors  []uuid.UUID
	targets []uuid.UUID
}

func (s *suspensionSessions) RevokeAll(_ context.Context, userID uuid.UUID) (time.Time, error) {
	s.revoked = append(s.revoked, userID)
	return time.Time{}, nil
}

func (s *suspensionSessions) RevokeAllBy(_ context.Context, actorID, userID uuid.UUID) (time.Time, error) {
	s.actors = append(s.actors, actorID)
	s.targets = append(s.targets, userID)
	return time.Time{}, nil
}
