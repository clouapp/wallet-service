package users_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/users"
)

type nameStore struct {
	users.Store
	names []string
	err   error
}

func (s *nameStore) UpdateFullName(_ context.Context, _ uuid.UUID, name string) error {
	if s.err != nil {
		return s.err
	}
	s.names = append(s.names, name)
	return nil
}

func TestUpdate_Profile_WritesTheNameOnTheLoadedUser(t *testing.T) {
	store := &nameStore{}
	user := &models.User{ID: uuid.New(), FullName: "Ada"}

	updated, err := users.NewService(users.Deps{Store: store}).UpdateProfile(context.Background(), user, users.ProfileInput{FullName: "Ada Lovelace"})

	require.NoError(t, err)
	assert.Equal(t, []string{"Ada Lovelace"}, store.names)
	assert.Same(t, user, updated)
	assert.Equal(t, "Ada Lovelace", updated.FullName)
}

func TestUpdate_Profile_LeavesABlankNameAsStored(t *testing.T) {
	store := &nameStore{}
	user := &models.User{ID: uuid.New(), FullName: "Ada"}

	updated, err := users.NewService(users.Deps{Store: store}).UpdateProfile(context.Background(), user, users.ProfileInput{})

	require.NoError(t, err)
	assert.Empty(t, store.names)
	assert.Equal(t, "Ada", updated.FullName)
}

func TestUpdate_Profile_ReturnsTheFailedWrite(t *testing.T) {
	store := &nameStore{err: errors.New("store down")}
	user := &models.User{ID: uuid.New(), FullName: "Ada"}

	_, err := users.NewService(users.Deps{Store: store}).UpdateProfile(context.Background(), user, users.ProfileInput{FullName: "Grace"})

	assert.ErrorIs(t, err, store.err)
	assert.Equal(t, "Ada", user.FullName, "a failed write leaves the loaded user as it was")
}
