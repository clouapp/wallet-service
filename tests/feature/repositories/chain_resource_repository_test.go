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

type ChainResourceRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.ChainResourceRepository
}

func TestChain_Resource_RepositorySuite(t *testing.T) {
	suite.Run(t, new(ChainResourceRepositoryTestSuite))
}

func (s *ChainResourceRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.Require().NoError(repositories.NewChainRepository(nil).Create(context.Background(), &models.Chain{
		ID: "eth", Name: "Ethereum", AdapterType: models.AdapterTypeEVM, NativeSymbol: "ETH",
		NativeDecimals: 18, RpcURL: "enc", RequiredConfirmations: 12, Status: "active",
	}))
	s.repo = repositories.NewChainResourceRepository(nil)
}

func (s *ChainResourceRepositoryTestSuite) TestFind_By_ChainAndType() {
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

func (s *ChainResourceRepositoryTestSuite) TestFind_By_ChainTypeAndNameIgnoresStatus() {
	disabled := &models.ChainResource{
		ID: uuid.New(), ChainID: "eth", Type: "explorer", Name: "Old explorer",
		URL: "https://example.invalid/explorer", Status: "disabled",
	}
	s.Require().NoError(s.repo.Create(context.Background(), disabled))

	found, err := s.repo.FindByChainTypeAndName(context.Background(), "eth", "explorer", "Old explorer")
	s.NoError(err)
	s.Equal(disabled.ID, found.ID)
	s.Equal("disabled", found.Status)

	missing, err := s.repo.FindByChainTypeAndName(context.Background(), "eth", "explorer", "missing")
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(missing)

	blank, err := s.repo.FindByChainTypeAndName(context.Background(), "eth", "", "Old explorer")
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(blank)
}
