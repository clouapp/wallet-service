package repositories_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/mocks"
)

type AccessTokenRepositoryTestSuite struct {
	suite.Suite
	repo    repositories.AccessTokenRepository
	accRepo repositories.AccountRepository
}

func TestAccessTokenRepositorySuite(t *testing.T) {
	suite.Run(t, new(AccessTokenRepositoryTestSuite))
}

func (s *AccessTokenRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewAccessTokenRepository()
	s.accRepo = repositories.NewAccountRepository()
}

func (s *AccessTokenRepositoryTestSuite) createAccount() uuid.UUID {
	acc := &models.Account{ID: uuid.New(), Name: "Token Acc", Status: "active"}
	s.Require().NoError(s.accRepo.Create(acc))
	return acc.ID
}

func (s *AccessTokenRepositoryTestSuite) TestCreate_Success() {
	accID := s.createAccount()
	token := &models.AccessToken{ID: uuid.New(), AccountID: accID, Name: "CI Token"}
	err := s.repo.Create(token)
	s.NoError(err)
}

func (s *AccessTokenRepositoryTestSuite) TestFindByAccountID() {
	accID := s.createAccount()
	s.Require().NoError(s.repo.Create(&models.AccessToken{ID: uuid.New(), AccountID: accID, Name: "T1"}))
	s.Require().NoError(s.repo.Create(&models.AccessToken{ID: uuid.New(), AccountID: accID, Name: "T2"}))

	tokens, err := s.repo.FindByAccountID(accID)
	s.NoError(err)
	s.Len(tokens, 2)
}

func (s *AccessTokenRepositoryTestSuite) TestFindByIDAndAccount_Found() {
	accID := s.createAccount()
	token := &models.AccessToken{ID: uuid.New(), AccountID: accID, Name: "Find Me"}
	s.Require().NoError(s.repo.Create(token))

	found, err := s.repo.FindByIDAndAccount(token.ID, accID)
	s.NoError(err)
	s.NotNil(found)
	s.Equal("Find Me", found.Name)
}

func (s *AccessTokenRepositoryTestSuite) TestFindByIDAndAccount_NotFound() {
	found, err := s.repo.FindByIDAndAccount(uuid.New(), uuid.New())
	s.NoError(err)
	s.Nil(found)
}

func (s *AccessTokenRepositoryTestSuite) TestFindByIDAndAccount_WrongAccount() {
	accID := s.createAccount()
	otherAccID := s.createAccount()
	token := &models.AccessToken{ID: uuid.New(), AccountID: accID, Name: "Mine"}
	s.Require().NoError(s.repo.Create(token))

	found, err := s.repo.FindByIDAndAccount(token.ID, otherAccID)
	s.NoError(err)
	s.Nil(found)
}

func (s *AccessTokenRepositoryTestSuite) TestDelete() {
	accID := s.createAccount()
	token := &models.AccessToken{ID: uuid.New(), AccountID: accID, Name: "To Delete"}
	s.Require().NoError(s.repo.Create(token))

	err := s.repo.Delete(token)
	s.NoError(err)

	found, err := s.repo.FindByIDAndAccount(token.ID, accID)
	s.NoError(err)
	s.Nil(found)
}
