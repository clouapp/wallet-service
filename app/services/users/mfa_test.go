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
	"github.com/stretchr/testify/assert"
)

func TestReset_MFA_ClearsTotpOnceAndLeavesTheUserActive(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	target := uuid.New()
	when := time.Date(2026, 10, 3, 20, 0, 0, 0, time.UTC)
	store := &mfaStore{user: &models.User{
		ID:          target,
		Status:      models.StatusActive,
		TotpEnabled: true,
		TotpSecret:  "sealed-marker",
		SuspendedAt: nil,
	}}
	activity := &suspensionActivity{}
	sessions := &suspensionSessions{}
	recovery := &mfaRecovery{count: 2}
	service := users.NewService(users.Deps{
		Store:    store,
		Activity: activity,
		Admins:   allowAdmins{actor},
		Sessions: sessions,
		Recovery: recovery,
		Clock:    func() time.Time { return when },
	})

	require.NoError(t, service.ResetMFA(context.Background(), actor, target))
	assert.False(t, store.user.TotpEnabled)
	assert.Empty(t, store.user.TotpSecret)
	assert.Nil(t, store.user.SuspendedAt)
	assert.Equal(t, 1, store.disabled)
	assert.Equal(t, 1, recovery.deleted)
	assert.Equal(t, int64(0), recovery.count)
	assert.Empty(t, sessions.revoked)
	assert.Equal(t, []uuid.UUID{actor}, sessions.actors)
	assert.Equal(t, []uuid.UUID{target}, sessions.targets)
	require.Len(t, activity.rows, 1)
	assert.Nil(t, activity.rows[0].AccountID)
	assert.Equal(t, actor, activity.rows[0].ActorUserID)
	assert.Equal(t, activitylog.ActionUserMFAReset, activity.rows[0].Action)
	assert.Equal(t, activitylog.TargetUser, activity.rows[0].TargetType)
	assert.Equal(t, target.String(), activity.rows[0].TargetID)
	encoded, err := activity.rows[0].Metadata.Encode()
	require.NoError(t, err)
	assert.JSONEq(t, `{"enabled":false,"key":"totp"}`, encoded)
	assert.NotContains(t, encoded, "sealed-marker")

	require.NoError(t, service.ResetMFA(context.Background(), actor, target))
	assert.Len(t, activity.rows, 1)
	assert.Equal(t, 1, store.disabled)
	assert.Equal(t, 1, recovery.deleted)
	assert.Equal(t, []uuid.UUID{actor}, sessions.actors)
	assert.Equal(t, []uuid.UUID{target}, sessions.targets)
}

func TestReset_MFA_OfAnAlreadyClearUserWritesNothing(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	target := uuid.New()
	store := &mfaStore{user: &models.User{ID: target, Status: models.StatusActive}}
	activity := &suspensionActivity{}
	service := users.NewService(users.Deps{
		Store:    store,
		Activity: activity,
		Admins:   allowAdmins{actor},
		Recovery: &mfaRecovery{},
	})

	require.NoError(t, service.ResetMFA(context.Background(), actor, target))
	assert.Empty(t, activity.rows)
	assert.Zero(t, store.disabled)
}

func TestReset_MFA_RefusesACallerWhoIsNotAPlatformAdmin(t *testing.T) {
	t.Parallel()

	target := uuid.New()
	store := &mfaStore{user: &models.User{
		ID:          target,
		TotpEnabled: true,
		TotpSecret:  "sealed-marker",
	}}
	activity := &suspensionActivity{}
	recovery := &mfaRecovery{count: 1}
	service := users.NewService(users.Deps{
		Store:    store,
		Activity: activity,
		Admins:   allowAdmins{},
		Recovery: recovery,
	})

	err := service.ResetMFA(context.Background(), uuid.New(), target)
	assert.ErrorIs(t, err, users.ErrMFAForbidden)
	assert.Equal(t, 1, store.finds)
	assert.True(t, store.user.TotpEnabled)
	assert.Equal(t, "sealed-marker", store.user.TotpSecret)
	assert.Equal(t, int64(1), recovery.count)
	assert.Empty(t, activity.rows)

	missing := &mfaStore{err: models.ErrRepositoryNotFound}
	err = users.NewService(users.Deps{
		Store:    missing,
		Activity: activity,
		Admins:   allowAdmins{},
		Recovery: recovery,
	}).ResetMFA(context.Background(), uuid.New(), uuid.New())
	assert.ErrorIs(t, err, users.ErrNotFound)
	assert.Equal(t, 1, missing.finds)
}

func TestReset_MFA_ReportsAMissingUserToAPlatformAdmin(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	store := &mfaStore{err: models.ErrRepositoryNotFound}
	service := users.NewService(users.Deps{
		Store:    store,
		Activity: &suspensionActivity{},
		Admins:   allowAdmins{actor},
		Recovery: &mfaRecovery{},
	})

	err := service.ResetMFA(context.Background(), actor, uuid.New())
	assert.ErrorIs(t, err, users.ErrNotFound)
	assert.Equal(t, 1, store.finds)
}

type mfaStore struct {
	user     *models.User
	err      error
	disabled int
	finds    int
}

func (s *mfaStore) FindByEmail(context.Context, string) (*models.User, error) {
	return nil, errors.New("unused")
}
func (s *mfaStore) FindByID(context.Context, uuid.UUID) (*models.User, error) {
	s.finds++
	if s.err != nil {
		return nil, s.err
	}
	return s.user, nil
}
func (s *mfaStore) Create(context.Context, *models.User) error { return errors.New("unused") }
func (s *mfaStore) UpdateDefaultAccountID(context.Context, uuid.UUID, *uuid.UUID) error {
	return errors.New("unused")
}
func (s *mfaStore) UpdateFullName(context.Context, uuid.UUID, string) error {
	return errors.New("unused")
}
func (s *mfaStore) UpdatePasswordHash(context.Context, uuid.UUID, string) error {
	return errors.New("unused")
}
func (s *mfaStore) UpdatePreferences(context.Context, uuid.UUID, *models.UserPreferences) error {
	return errors.New("unused")
}
func (s *mfaStore) UpdateTotpSecret(context.Context, uuid.UUID, string) error {
	return errors.New("unused")
}
func (s *mfaStore) EnableTotp(context.Context, uuid.UUID) error { return errors.New("unused") }
func (s *mfaStore) DisableTotp(_ context.Context, id uuid.UUID) error {
	if s.user == nil || s.user.ID != id {
		return errors.New("missing user")
	}
	s.disabled++
	s.user.TotpEnabled = false
	s.user.TotpSecret = ""
	return nil
}
func (s *mfaStore) SetSuspendedAt(context.Context, uuid.UUID, *time.Time) error {
	return errors.New("unused")
}
func (s *mfaStore) List(context.Context, int, int) ([]models.User, int64, error) {
	return nil, 0, errors.New("unused")
}

type mfaRecovery struct {
	count   int64
	deleted int
}

func (r *mfaRecovery) FindUnusedByUserID(context.Context, uuid.UUID) ([]models.TotpRecoveryCode, error) {
	return nil, errors.New("unused")
}
func (r *mfaRecovery) MarkUsed(context.Context, uuid.UUID) error { return errors.New("unused") }
func (r *mfaRecovery) CreateBatch(context.Context, []models.TotpRecoveryCode) error {
	return errors.New("unused")
}
func (r *mfaRecovery) DeleteByUserID(context.Context, uuid.UUID) error {
	r.deleted++
	r.count = 0
	return nil
}
func (r *mfaRecovery) CountByUserID(context.Context, uuid.UUID) (int64, error) {
	return r.count, nil
}
