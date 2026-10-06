package repositories_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type ChainRegistryRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.ChainRegistryRepository
}

func TestChain_Registry_RepositorySuite(t *testing.T) {
	suite.Run(t, new(ChainRegistryRepositoryTestSuite))
}

func (s *ChainRegistryRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.repo = repositories.NewChainRegistryRepository(nil)
}

func (s *ChainRegistryRepositoryTestSuite) TestFind_Account_NotFound() {
	found, err := s.repo.FindAccount(context.Background(), uuid.New())
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *ChainRegistryRepositoryTestSuite) TestChainRegistryRepository_Find_Account() {
	account := fixtures.InsertAccount(s.T(), "registry")

	found, err := s.repo.FindAccount(context.Background(), account.ID)
	s.NoError(err)
	s.Require().NotNil(found)
	s.Equal(account.ID, found.ID)
	s.Equal(account.Environment, found.Environment)
}

func (s *ChainRegistryRepositoryTestSuite) TestUpdate_Chain_NetworkNotFound() {
	err := s.repo.UpdateChainNetwork(context.Background(), "missing", nil, true)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
}

func (s *ChainRegistryRepositoryTestSuite) TestUpdate_Account_EnvironmentNotFound() {
	err := s.repo.UpdateAccountEnvironment(context.Background(), uuid.New(), models.EnvironmentTest)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
}

func (s *ChainRegistryRepositoryTestSuite) TestUpdate_Account_Environment() {
	account := fixtures.InsertAccount(s.T(), "registry-env")

	s.NoError(s.repo.UpdateAccountEnvironment(context.Background(), account.ID, models.EnvironmentTest))

	found, err := s.repo.FindAccount(context.Background(), account.ID)
	s.NoError(err)
	s.Equal(models.EnvironmentTest, found.Environment)
}

func (s *ChainRegistryRepositoryTestSuite) TestChain_Holds_BalanceIsFalseWhenNothingIsCached() {
	holds, err := s.repo.ChainHoldsBalance(context.Background(), models.ChainETH)
	s.NoError(err)
	s.False(holds)
}
