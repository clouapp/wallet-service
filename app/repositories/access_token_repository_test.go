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

func (s *AccessTokenRepositoryTestSuite) TestDeleteByAccountAndCreator_LeavesOtherTokens() {
	accID := s.createAccount()
	otherAccount := s.createAccount()
	creator := uuid.New()
	otherCreator := uuid.New()
	mine := &models.AccessToken{ID: uuid.New(), AccountID: accID, CreatedBy: &creator, Name: "Mine", SpendingLimit: "{}"}
	theirs := &models.AccessToken{ID: uuid.New(), AccountID: accID, CreatedBy: &otherCreator, Name: "Theirs", SpendingLimit: "{}"}
	elsewhere := &models.AccessToken{ID: uuid.New(), AccountID: otherAccount, CreatedBy: &creator, Name: "Elsewhere", SpendingLimit: "{}"}
	s.Require().NoError(s.repo.Create(context.Background(), mine))
	s.Require().NoError(s.repo.Create(context.Background(), theirs))
	s.Require().NoError(s.repo.Create(context.Background(), elsewhere))

	s.Require().NoError(s.repo.DeleteByAccountAndCreator(context.Background(), accID, creator))

	_, err := s.repo.FindByIDAndAccount(context.Background(), mine.ID, accID)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	found, err := s.repo.FindByIDAndAccount(context.Background(), theirs.ID, accID)
	s.Require().NoError(err)
	s.Equal("Theirs", found.Name)
	found, err = s.repo.FindByIDAndAccount(context.Background(), elsewhere.ID, otherAccount)
	s.Require().NoError(err)
	s.Equal("Elsewhere", found.Name)
}

func (s *AccessTokenRepositoryTestSuite) TestRecordUseSetsLastUsedAtAndSkipsRevoked() {
	accID := s.createAccount()
	active := &models.AccessToken{ID: uuid.New(), AccountID: accID, Name: "Active"}
	revoked := &models.AccessToken{ID: uuid.New(), AccountID: accID, Name: "Revoked"}
	s.Require().NoError(s.repo.Create(context.Background(), active))
	s.Require().NoError(s.repo.Create(context.Background(), revoked))
	s.Require().NoError(s.repo.MarkRevoked(context.Background(), revoked.ID, accID))

	s.Require().NoError(s.repo.RecordUse(context.Background(), active.ID, accID))
	s.Require().NoError(s.repo.RecordUse(context.Background(), revoked.ID, accID))

	found, err := s.repo.FindByIDAndAccount(context.Background(), active.ID, accID)
	s.Require().NoError(err)
	s.NotNil(found.LastUsedAt)
	s.Nil(found.RevokedAt)

	found, err = s.repo.FindByIDAndAccount(context.Background(), revoked.ID, accID)
	s.Require().NoError(err)
	s.Nil(found.LastUsedAt)
	s.NotNil(found.RevokedAt)
}

func (s *AccessTokenRepositoryTestSuite) TestMarkRevokedKeepsTheRowAndTheFirstStamp() {
	accID := s.createAccount()
	token := &models.AccessToken{ID: uuid.New(), AccountID: accID, Name: "Keep"}
	s.Require().NoError(s.repo.Create(context.Background(), token))
	s.Require().NoError(s.repo.MarkRevoked(context.Background(), token.ID, accID))

	first, err := s.repo.FindByIDAndAccount(context.Background(), token.ID, accID)
	s.Require().NoError(err)
	s.Require().NotNil(first.RevokedAt)

	s.Require().NoError(s.repo.MarkRevoked(context.Background(), token.ID, accID))
	second, err := s.repo.FindByIDAndAccount(context.Background(), token.ID, accID)
	s.Require().NoError(err)
	s.True(first.RevokedAt.Equal(*second.RevokedAt))
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
