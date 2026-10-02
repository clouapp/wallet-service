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

type ChainResourceRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.ChainResourceRepository
}

func TestChainResourceRepositorySuite(t *testing.T) {
	suite.Run(t, new(ChainResourceRepositoryTestSuite))
}

func (s *ChainResourceRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.Require().NoError(repositories.NewChainRepository(nil).Create(context.Background(), &models.Chain{
		ID: "eth", Name: "Ethereum", AdapterType: models.AdapterTypeEVM, NativeSymbol: "ETH",
		NativeDecimals: 18, RpcURL: "enc", RequiredConfirmations: 12, Status: "active",
	}))
	s.repo = repositories.NewChainResourceRepository(nil)
}

func (s *ChainResourceRepositoryTestSuite) TestFindByChainAndType() {
	explorer := &models.ChainResource{
		ID: uuid.New(), ChainID: "eth", Type: "explorer", Name: "Etherscan",
		URL: "https://etherscan.io", Status: "active", DisplayOrder: 1,
	}
	faucet := &models.ChainResource{
		ID: uuid.New(), ChainID: "eth", Type: "faucet", Name: "Faucet",
		URL: "https://faucet.example", Status: "active", DisplayOrder: 2,
	}
	s.Require().NoError(s.repo.Create(context.Background(), explorer))
	s.Require().NoError(s.repo.Create(context.Background(), faucet))

	all, err := s.repo.FindByChainID(context.Background(), "eth")
	s.NoError(err)
	s.Len(all, 2)

	explorers, err := s.repo.FindByChainAndType(context.Background(), "eth", "explorer")
	s.NoError(err)
	s.Len(explorers, 1)
	s.Equal("Etherscan", explorers[0].Name)
}
