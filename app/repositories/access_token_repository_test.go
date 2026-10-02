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

type AccessTokenRepositoryTestSuite struct {
	suite.Suite
	repo    *repositories.AccessTokenRepository
	accRepo *repositories.AccountRepository
}

func TestAccessTokenRepositorySuite(t *testing.T) {
	suite.Run(t, new(AccessTokenRepositoryTestSuite))
}

func (s *AccessTokenRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewAccessTokenRepository(nil)
	s.accRepo = repositories.NewAccountRepository(nil)
}

func (s *AccessTokenRepositoryTestSuite) createAccount() uuid.UUID {
	acc := &models.Account{ID: uuid.New(), Name: "Token Acc", Status: "active"}
	s.Require().NoError(s.accRepo.Create(context.Background(), acc))
	return acc.ID
}

func (s *AccessTokenRepositoryTestSuite) TestCreate_Success() {
	accID := s.createAccount()
	token := &models.AccessToken{ID: uuid.New(), AccountID: accID, Name: "CI Token"}
	err := s.repo.Create(context.Background(), token)
	s.NoError(err)
}

func (s *AccessTokenRepositoryTestSuite) TestFindByAccountID() {
	accID := s.createAccount()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccessToken{ID: uuid.New(), AccountID: accID, Name: "T1"}))
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccessToken{ID: uuid.New(), AccountID: accID, Name: "T2"}))

	tokens, err := s.repo.FindByAccountID(context.Background(), accID)
	s.NoError(err)
	s.Len(tokens, 2)
}

func (s *AccessTokenRepositoryTestSuite) TestFindByIDAndAccount_Found() {
	accID := s.createAccount()
	token := &models.AccessToken{ID: uuid.New(), AccountID: accID, Name: "Find Me"}
	s.Require().NoError(s.repo.Create(context.Background(), token))

	found, err := s.repo.FindByIDAndAccount(context.Background(), token.ID, accID)
	s.NoError(err)
	s.NotNil(found)
	s.Equal("Find Me", found.Name)
}

func (s *AccessTokenRepositoryTestSuite) TestFindByIDAndAccount_NotFound() {
	found, err := s.repo.FindByIDAndAccount(context.Background(), uuid.New(), uuid.New())
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *AccessTokenRepositoryTestSuite) TestFindByIDAndAccount_WrongAccount() {
	accID := s.createAccount()
	otherAccID := s.createAccount()
	token := &models.AccessToken{ID: uuid.New(), AccountID: accID, Name: "Mine"}
	s.Require().NoError(s.repo.Create(context.Background(), token))

	found, err := s.repo.FindByIDAndAccount(context.Background(), token.ID, otherAccID)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *AccessTokenRepositoryTestSuite) TestDelete() {
	accID := s.createAccount()
	token := &models.AccessToken{ID: uuid.New(), AccountID: accID, Name: "To Delete"}
	s.Require().NoError(s.repo.Create(context.Background(), token))

	err := s.repo.Delete(context.Background(), token)
	s.NoError(err)

	found, err := s.repo.FindByIDAndAccount(context.Background(), token.ID, accID)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}
