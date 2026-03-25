package repositories_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/mocks"
)

type UserRepositoryTestSuite struct {
	suite.Suite
	repo repositories.UserRepository
}

func TestUserRepositorySuite(t *testing.T) {
	suite.Run(t, new(UserRepositoryTestSuite))
}

func (s *UserRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewUserRepository()
}

func (s *UserRepositoryTestSuite) TestCreate_Success() {
	user := &models.User{
		ID:           uuid.New(),
		Email:        "test@example.com",
		PasswordHash: "hashed_pw",
		Status:       "active",
	}
	err := s.repo.Create(user)
	s.NoError(err)

	found, err := s.repo.FindByEmail("test@example.com")
	s.NoError(err)
	s.NotNil(found)
	s.Equal(user.ID, found.ID)
	s.Equal("test@example.com", found.Email)
}

func (s *UserRepositoryTestSuite) TestFindByEmail_Found() {
	user := &models.User{
		ID:           uuid.New(),
		Email:        "found@example.com",
		PasswordHash: "hash",
		Status:       "active",
	}
	s.Require().NoError(s.repo.Create(user))

	found, err := s.repo.FindByEmail("found@example.com")
	s.NoError(err)
	s.NotNil(found)
	s.Equal(user.ID, found.ID)
}

func (s *UserRepositoryTestSuite) TestFindByEmail_NotFound() {
	found, err := s.repo.FindByEmail("nonexistent@example.com")
	s.NoError(err)
	s.Nil(found)
}

func (s *UserRepositoryTestSuite) TestFindByID_Found() {
	user := &models.User{
		ID:           uuid.New(),
		Email:        "byid@example.com",
		PasswordHash: "hash",
		Status:       "active",
	}
	s.Require().NoError(s.repo.Create(user))

	found, err := s.repo.FindByID(user.ID)
	s.NoError(err)
	s.NotNil(found)
	s.Equal("byid@example.com", found.Email)
}

func (s *UserRepositoryTestSuite) TestFindByID_NotFound() {
	found, err := s.repo.FindByID(uuid.New())
	s.NoError(err)
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
	s.Require().NoError(s.repo.Create(user))

	err := s.repo.UpdateFullName(user.ID, "New Name")
	s.NoError(err)

	found, err := s.repo.FindByID(user.ID)
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
	s.Require().NoError(s.repo.Create(user))

	err := s.repo.UpdatePasswordHash(user.ID, "new_hash")
	s.NoError(err)

	found, err := s.repo.FindByID(user.ID)
	s.NoError(err)
	s.Equal("new_hash", found.PasswordHash)
}
