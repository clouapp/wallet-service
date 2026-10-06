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

type ChainRegistryRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.ChainRegistryRepository
}

func TestChainRegistryRepositorySuite(t *testing.T) {
	suite.Run(t, new(ChainRegistryRepositoryTestSuite))
}

func (s *ChainRegistryRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewChainRegistryRepository(nil)
}

func (s *ChainRegistryRepositoryTestSuite) TestFindAccountNotFound() {
	found, err := s.repo.FindAccount(context.Background(), uuid.New())
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *ChainRegistryRepositoryTestSuite) TestFindAccount() {
	account := mocks.InsertAccount(s.T(), "registry")

	found, err := s.repo.FindAccount(context.Background(), account.ID)
	s.NoError(err)
	s.Require().NotNil(found)
	s.Equal(account.ID, found.ID)
	s.Equal(account.Environment, found.Environment)
}

func (s *ChainRegistryRepositoryTestSuite) TestUpdateChainNetworkNotFound() {
	err := s.repo.UpdateChainNetwork(context.Background(), "missing", nil, true)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
}

func (s *ChainRegistryRepositoryTestSuite) TestUpdateAccountEnvironmentNotFound() {
	err := s.repo.UpdateAccountEnvironment(context.Background(), uuid.New(), models.EnvironmentTest)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
}

func (s *ChainRegistryRepositoryTestSuite) TestUpdateAccountEnvironment() {
	account := mocks.InsertAccount(s.T(), "registry-env")

	s.NoError(s.repo.UpdateAccountEnvironment(context.Background(), account.ID, models.EnvironmentTest))

	found, err := s.repo.FindAccount(context.Background(), account.ID)
	s.NoError(err)
	s.Equal(models.EnvironmentTest, found.Environment)
}

func (s *ChainRegistryRepositoryTestSuite) TestChainHoldsBalanceIsFalseWhenNothingIsCached() {
	holds, err := s.repo.ChainHoldsBalance(context.Background(), models.ChainETH)
	s.NoError(err)
	s.False(holds)
}
