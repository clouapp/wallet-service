package repositories_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/mocks"
)

type UserRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.UserRepository
}

func TestUserRepositorySuite(t *testing.T) {
	suite.Run(t, new(UserRepositoryTestSuite))
}

func (s *UserRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewUserRepository(nil)
}

func (s *UserRepositoryTestSuite) TestCreate_Success() {
	user := &models.User{
		ID:           uuid.New(),
		Email:        "test@example.com",
		PasswordHash: "hashed_pw",
		Status:       "active",
	}
	err := s.repo.Create(context.Background(), user)
	s.NoError(err)
	s.NotNil(user.Preferences)

	found, err := s.repo.FindByEmail(context.Background(), "test@example.com")
	s.NoError(err)
	s.NotNil(found)
	s.Equal(user.ID, found.ID)
	s.Equal("test@example.com", found.Email)
	s.NotNil(found.Preferences)
}

func (s *UserRepositoryTestSuite) TestFindByEmail_Found() {
	user := &models.User{
		ID:           uuid.New(),
		Email:        "found@example.com",
		PasswordHash: "hash",
		Status:       "active",
	}
	s.Require().NoError(s.repo.Create(context.Background(), user))

	found, err := s.repo.FindByEmail(context.Background(), "found@example.com")
	s.NoError(err)
	s.NotNil(found)
	s.Equal(user.ID, found.ID)
}

func (s *UserRepositoryTestSuite) TestFindByEmail_NotFound() {
	found, err := s.repo.FindByEmail(context.Background(), "nonexistent@example.com")
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *UserRepositoryTestSuite) TestFindByID_Found() {
	user := &models.User{
		ID:           uuid.New(),
		Email:        "byid@example.com",
		PasswordHash: "hash",
		Status:       "active",
	}
	s.Require().NoError(s.repo.Create(context.Background(), user))

	found, err := s.repo.FindByID(context.Background(), user.ID)
	s.NoError(err)
	s.NotNil(found)
	s.Equal("byid@example.com", found.Email)
}

func (s *UserRepositoryTestSuite) TestFindByID_NotFound() {
	found, err := s.repo.FindByID(context.Background(), uuid.New())
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *UserRepositoryTestSuite) TestUpdateFullName() {
	user := &models.User{
		ID:           uuid.New(),
		Email:        "name@example.com",
		PasswordHash: "hash",
		FullName:     "Old Name",
		Status:       "active",
	}
	s.Require().NoError(s.repo.Create(context.Background(), user))

	err := s.repo.UpdateFullName(context.Background(), user.ID, "New Name")
	s.NoError(err)

	found, err := s.repo.FindByID(context.Background(), user.ID)
	s.NoError(err)
	s.Equal("New Name", found.FullName)
}

func (s *UserRepositoryTestSuite) TestUpdatePasswordHash() {
	user := &models.User{
		ID:           uuid.New(),
		Email:        "pw@example.com",
		PasswordHash: "old_hash",
		Status:       "active",
	}
	s.Require().NoError(s.repo.Create(context.Background(), user))

	err := s.repo.UpdatePasswordHash(context.Background(), user.ID, "new_hash")
	s.NoError(err)

	found, err := s.repo.FindByID(context.Background(), user.ID)
	s.NoError(err)
	s.Equal("new_hash", found.PasswordHash)
}

func (s *UserRepositoryTestSuite) TestAdvanceTotpCounter_OnlyMovesForward() {
	userID := insertActiveUserRow(s.T())

	advanced, err := s.repo.AdvanceTotpCounter(context.Background(), userID, 100)
	s.Require().NoError(err)
	s.True(advanced, "a newer step is recorded")

	advanced, err = s.repo.AdvanceTotpCounter(context.Background(), userID, 100)
	s.Require().NoError(err)
	s.False(advanced, "the same step is a replay")

	advanced, err = s.repo.AdvanceTotpCounter(context.Background(), userID, 99)
	s.Require().NoError(err)
	s.False(advanced, "an older step is a replay")

	found, err := s.repo.FindByID(context.Background(), userID)
	s.Require().NoError(err)
	s.Equal(int64(100), found.TotpLastUsedCounter)
}

func (s *UserRepositoryTestSuite) TestUpdateSessionsRevokedAt() {
	userID := insertActiveUserRow(s.T())
	found, err := s.repo.FindByID(context.Background(), userID)
	s.Require().NoError(err)
	s.Nil(found.SessionsRevokedAt, "no watermark until the first revocation")

	watermark := time.Date(2026, 10, 2, 12, 0, 6, 0, time.UTC)
	s.Require().NoError(s.repo.UpdateSessionsRevokedAt(context.Background(), userID, watermark))

	found, err = s.repo.FindByID(context.Background(), userID)
	s.Require().NoError(err)
	s.Require().NotNil(found.SessionsRevokedAt)
	s.True(watermark.Equal(*found.SessionsRevokedAt))
}

func (s *UserRepositoryTestSuite) TestSetSuspendedAtSetsAndClearsTheColumn() {
	userID := insertActiveUserRow(s.T())
	found, err := s.repo.FindByID(context.Background(), userID)
	s.Require().NoError(err)
	s.Nil(found.SuspendedAt)

	at := time.Date(2026, 10, 3, 18, 4, 0, 0, time.UTC)
	s.Require().NoError(s.repo.SetSuspendedAt(context.Background(), userID, &at))
	found, err = s.repo.FindByID(context.Background(), userID)
	s.Require().NoError(err)
	s.Require().NotNil(found.SuspendedAt)
	s.True(at.Equal(found.SuspendedAt.UTC()))
	s.Nil(found.SuspensionReason)

	s.Require().NoError(s.repo.SetSuspendedAt(context.Background(), userID, nil))
	found, err = s.repo.FindByID(context.Background(), userID)
	s.Require().NoError(err)
	s.Nil(found.SuspendedAt)
	s.EqualError(s.repo.SetSuspendedAt(context.Background(), uuid.Nil, &at), "set suspended at: user id is required")
}
