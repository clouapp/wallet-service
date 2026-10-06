package users_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/users"
	"github.com/stretchr/testify/assert"
)

func TestList_Refuses_ACallerWhoIsNotAPlatformAdmin(t *testing.T) {
	store := &listStore{rows: []models.User{{ID: uuid.New(), Email: "hidden@example.com", PasswordHash: "hash"}}}
	service := users.NewService(users.Deps{Store: store, Admins: allowAdmins{}})

	rows, total, err := service.List(context.Background(), uuid.New(), 20, 0)

	assert.ErrorIs(t, err, users.ErrViewForbidden)
	assert.Nil(t, rows)
	assert.Zero(t, total)
	assert.False(t, store.called)
}

func TestList_Returns_ThePageForAPlatformAdmin(t *testing.T) {
	actor := uuid.New()
	newer := models.User{ID: uuid.New(), Email: "newer@example.com", Status: models.StatusActive}
	store := &listStore{rows: []models.User{newer}, total: 4}
	service := users.NewService(users.Deps{Store: store, Admins: allowAdmins{actor}})

	rows, total, err := service.List(context.Background(), actor, 1, 2)

	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	assert.Equal(t, []models.User{newer}, rows)
	assert.Equal(t, 1, store.limit)
	assert.Equal(t, 2, store.offset)
}

func TestList_Rejects_AMissingActorAndABadPage(t *testing.T) {
	actor := uuid.New()
	store := &listStore{}
	service := users.NewService(users.Deps{Store: store, Admins: allowAdmins{actor}})

	_, _, err := service.List(nil, actor, 20, 0)
	assert.ErrorContains(t, err, "context is required")

	_, _, err = service.List(context.Background(), uuid.Nil, 20, 0)
	assert.ErrorContains(t, err, "actor is required")

	_, _, err = service.List(context.Background(), actor, 0, 0)
	assert.ErrorContains(t, err, "limit and offset are invalid")

	_, _, err = service.List(context.Background(), actor, 20, -1)
	assert.ErrorContains(t, err, "limit and offset are invalid")
	assert.False(t, store.called)

	_, _, err = users.NewService(users.Deps{Store: store}).List(context.Background(), actor, 20, 0)
	assert.ErrorContains(t, err, "platform admins are required")
}

type listStore struct {
	rows   []models.User
	total  int64
	limit  int
	offset int
	called bool
	err    error
}

func (s *listStore) FindByEmail(context.Context, string) (*models.User, error) {
	return nil, errors.New("unused")
}
func (s *listStore) FindByID(context.Context, uuid.UUID) (*models.User, error) {
	return nil, errors.New("unused")
}
func (s *listStore) Create(context.Context, *models.User) error { return errors.New("unused") }
func (s *listStore) UpdateDefaultAccountID(context.Context, uuid.UUID, *uuid.UUID) error {
	return errors.New("unused")
}
func (s *listStore) UpdateFullName(context.Context, uuid.UUID, string) error {
	return errors.New("unused")
}
func (s *listStore) UpdatePasswordHash(context.Context, uuid.UUID, string) error {
	return errors.New("unused")
}
func (s *listStore) UpdatePreferences(context.Context, uuid.UUID, *models.UserPreferences) error {
	return errors.New("unused")
}
func (s *listStore) UpdateTotpSecret(context.Context, uuid.UUID, string) error {
	return errors.New("unused")
}
func (s *listStore) EnableTotp(context.Context, uuid.UUID) error  { return errors.New("unused") }
func (s *listStore) DisableTotp(context.Context, uuid.UUID) error { return errors.New("unused") }
func (s *listStore) SetSuspendedAt(context.Context, uuid.UUID, *time.Time) error {
	return errors.New("unused")
}
func (s *listStore) List(_ context.Context, limit, offset int) ([]models.User, int64, error) {
	s.called = true
	s.limit = limit
	s.offset = offset
	if s.err != nil {
		return nil, 0, s.err
	}
	return s.rows, s.total, nil
}
