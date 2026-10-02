package repositories_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/mocks"
)

type AccountUserRepositoryTestSuite struct {
	suite.Suite
	repo    *repositories.AccountUserRepository
	accRepo *repositories.AccountRepository
}

func TestAccountUserRepositorySuite(t *testing.T) {
	suite.Run(t, new(AccountUserRepositoryTestSuite))
}

func (s *AccountUserRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewAccountUserRepository(nil)
	s.accRepo = repositories.NewAccountRepository(nil)
}

func (s *AccountUserRepositoryTestSuite) createAccount() uuid.UUID {
	acc := &models.Account{ID: uuid.New(), Name: "Acc " + uuid.NewString()[:8], Status: "active"}
	s.Require().NoError(s.accRepo.Create(context.Background(), acc))
	return acc.ID
}

func (s *AccountUserRepositoryTestSuite) TestCreate_Success() {
	accID := s.createAccount()
	au := &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: uuid.New(), Role: "owner"}
	err := s.repo.Create(context.Background(), au)
	s.NoError(err)
}

func (s *AccountUserRepositoryTestSuite) TestFindByAccountID() {
	accID := s.createAccount()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: uuid.New(), Role: "owner"}))
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: uuid.New(), Role: "admin"}))

	members, err := s.repo.FindByAccountID(context.Background(), accID)
	s.NoError(err)
	s.Len(members, 2)
}

func (s *AccountUserRepositoryTestSuite) TestFindByAccountAndUser_Found() {
	accID := s.createAccount()
	userID := uuid.New()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: userID, Role: "admin"}))

	au, err := s.repo.FindByAccountAndUser(context.Background(), accID, userID)
	s.NoError(err)
	s.NotNil(au)
	s.Equal("admin", au.Role)
}

func (s *AccountUserRepositoryTestSuite) TestFindByAccountAndUser_NotFound() {
	au, err := s.repo.FindByAccountAndUser(context.Background(), uuid.New(), uuid.New())
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(au)
}

func (s *AccountUserRepositoryTestSuite) TestFindByAccountAndUserIncludeDeleted() {
	accID := s.createAccount()
	userID := uuid.New()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: userID, Role: "admin"}))

	err := s.repo.SoftDeleteByAccountAndUser(context.Background(), accID, userID)
	s.Require().NoError(err)

	active, err := s.repo.FindByAccountAndUser(context.Background(), accID, userID)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(active)

	withDeleted, err := s.repo.FindByAccountAndUserIncludeDeleted(context.Background(), accID, userID)
	s.NoError(err)
	s.NotNil(withDeleted)
	s.NotNil(withDeleted.DeletedAt)
}

func (s *AccountUserRepositoryTestSuite) TestFindByUserID() {
	acc1 := s.createAccount()
	acc2 := s.createAccount()
	userID := uuid.New()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: acc1, UserID: userID, Role: "owner"}))
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: acc2, UserID: userID, Role: "admin"}))

	memberships, err := s.repo.FindByUserID(context.Background(), userID)
	s.NoError(err)
	s.Len(memberships, 2)
}

func (s *AccountUserRepositoryTestSuite) TestSetRole() {
	accID := s.createAccount()
	userID := uuid.New()
	au := &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: userID, Role: "auditor"}
	s.Require().NoError(s.repo.Create(context.Background(), au))

	err := s.repo.SetRole(context.Background(), au.ID, "admin")
	s.NoError(err)

	found, err := s.repo.FindByAccountAndUser(context.Background(), accID, userID)
	s.NoError(err)
	s.Equal("admin", found.Role)
}

func (s *AccountUserRepositoryTestSuite) TestSoftDeleteByAccountAndUser() {
	accID := s.createAccount()
	userID := uuid.New()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: userID, Role: "user"}))

	err := s.repo.SoftDeleteByAccountAndUser(context.Background(), accID, userID)
	s.NoError(err)

	found, err := s.repo.FindByAccountAndUser(context.Background(), accID, userID)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}
