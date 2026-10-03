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
)

func TestUpdatePreferencesForwardsToTheStore(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	id := uuid.New()
	prefs := &models.UserPreferences{}
	err := users.NewService(store).UpdatePreferences(context.Background(), id, prefs)
	require.NoError(t, err)
	require.Equal(t, id, store.id)
	require.Same(t, prefs, store.prefs)

	store.err = errors.New("store down")
	err = users.NewService(store).UpdatePreferences(context.Background(), id, prefs)
	require.ErrorIs(t, err, store.err)
}

func TestUserWritesRequireContextAndStore(t *testing.T) {
	t.Parallel()

	err := users.NewService(&fakeStore{}).UpdatePreferences(nil, uuid.New(), &models.UserPreferences{})
	require.EqualError(t, err, "update preferences: context is required")

	err = users.NewService(nil).UpdateFullName(context.Background(), uuid.New(), "Ada")
	require.EqualError(t, err, "users service: users repository is required")
}

type fakeStore struct {
	id    uuid.UUID
	prefs *models.UserPreferences
	err   error
}

func (f *fakeStore) FindByEmail(context.Context, string) (*models.User, error) { return nil, f.err }
func (f *fakeStore) FindByID(context.Context, uuid.UUID) (*models.User, error) { return nil, f.err }
func (f *fakeStore) Create(context.Context, *models.User) error                { return f.err }
func (f *fakeStore) UpdateDefaultAccountID(context.Context, uuid.UUID, *uuid.UUID) error {
	return f.err
}
func (f *fakeStore) UpdateFullName(context.Context, uuid.UUID, string) error { return f.err }
func (f *fakeStore) UpdatePasswordHash(context.Context, uuid.UUID, string) error {
	return f.err
}
func (f *fakeStore) UpdatePreferences(_ context.Context, id uuid.UUID, prefs *models.UserPreferences) error {
	f.id = id
	f.prefs = prefs
	return f.err
}
func (f *fakeStore) UpdateTotpSecret(context.Context, uuid.UUID, string) error { return f.err }
func (f *fakeStore) EnableTotp(context.Context, uuid.UUID) error               { return f.err }
func (f *fakeStore) DisableTotp(context.Context, uuid.UUID) error              { return f.err }
func (f *fakeStore) SetSuspendedAt(context.Context, uuid.UUID, *time.Time) error {
	return f.err
}
