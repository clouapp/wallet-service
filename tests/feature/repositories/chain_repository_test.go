package repositories_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type ChainRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.ChainRepository
}

func TestChainRepositorySuite(t *testing.T) {
	suite.Run(t, new(ChainRepositoryTestSuite))
}

func (s *ChainRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.repo = repositories.NewChainRepository(nil)
}

func (s *ChainRepositoryTestSuite) chain(id string, testnet bool, status string) *models.Chain {
	return &models.Chain{
		ID: id, Name: id, AdapterType: models.AdapterTypeEVM, NativeSymbol: "ETH",
		NativeDecimals: 18, RpcURL: "enc", RequiredConfirmations: 1,
		IsTestnet: testnet, Status: status, DisplayOrder: 1,
	}
}

func (s *ChainRepositoryTestSuite) TestCreateAndFindByID() {
	chain := s.chain("teth", true, "active")
	s.Require().NoError(s.repo.Create(context.Background(), chain))

	found, err := s.repo.FindByID(context.Background(), "teth")
	s.NoError(err)
	s.Equal("teth", found.ID)
	s.True(found.IsTestnet)
}

func (s *ChainRepositoryTestSuite) TestFindByID_NotFound() {
	found, err := s.repo.FindByID(context.Background(), "missing")
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *ChainRepositoryTestSuite) TestFindByID_Empty() {
	found, err := s.repo.FindByID(context.Background(), "")
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *ChainRepositoryTestSuite) TestFindActiveAndByTestnet() {
	s.Require().NoError(s.repo.Create(context.Background(), s.chain("eth", false, "active")))
	s.Require().NoError(s.repo.Create(context.Background(), s.chain("teth", true, "active")))
	s.Require().NoError(s.repo.Create(context.Background(), s.chain("gone", false, "disabled")))

	active, err := s.repo.FindActive(context.Background())
	s.NoError(err)
	s.Len(active, 2)

	testnets, err := s.repo.FindByTestnet(context.Background(), true)
	s.NoError(err)
	s.Len(testnets, 1)
	s.Equal("teth", testnets[0].ID)
}
