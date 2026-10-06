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

type TokenRepositoryTestSuite struct {
	suite.Suite
	chains *repositories.ChainRepository
	repo   *repositories.TokenRepository
}

func TestToken_Repository_Suite(t *testing.T) {
	suite.Run(t, new(TokenRepositoryTestSuite))
}

func (s *TokenRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.chains = repositories.NewChainRepository(nil)
	s.repo = repositories.NewTokenRepository(nil)
	s.Require().NoError(s.chains.Create(context.Background(), &models.Chain{
		ID: "eth", Name: "Ethereum", AdapterType: models.AdapterTypeEVM, NativeSymbol: "ETH",
		NativeDecimals: 18, RpcURL: "enc", RequiredConfirmations: 12, Status: "active",
	}))
}

func (s *TokenRepositoryTestSuite) TestFind_ByChainID_OnlyActive() {
	active := &models.Token{
		ID: uuid.New(), ChainID: "eth", Symbol: "USDC", Name: "USD Coin",
		ContractAddress: "0xabc", Decimals: 6, Status: "active",
	}
	disabled := &models.Token{
		ID: uuid.New(), ChainID: "eth", Symbol: "OLD", Name: "Old",
		ContractAddress: "0xdef", Decimals: 18, Status: "disabled",
	}
	s.Require().NoError(s.repo.Create(context.Background(), active))
	s.Require().NoError(s.repo.Create(context.Background(), disabled))

	found, err := s.repo.FindByChainID(context.Background(), "eth")
	s.Require().NoError(err)
	s.Require().Len(found, 1)
	s.Equal("USDC", found[0].Symbol)

	all, err := s.repo.FindActive(context.Background())
	s.Require().NoError(err)
	s.Len(all, 1)
}

func (s *TokenRepositoryTestSuite) TestFind_ByID_NotFound() {
	found, err := s.repo.FindByID(context.Background(), uuid.New())
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *TokenRepositoryTestSuite) TestFind_By_ChainAndContractIgnoresStatus() {
	disabled := &models.Token{
		ID: uuid.New(), ChainID: "eth", Symbol: "USDC", Name: "USD Coin",
		ContractAddress: "0xabc", Decimals: 6, Status: "disabled",
	}
	s.Require().NoError(s.repo.Create(context.Background(), disabled))

	found, err := s.repo.FindByChainAndContract(context.Background(), "eth", "0xabc")
	s.Require().NoError(err)
	s.Equal(disabled.ID, found.ID)
	s.Equal("disabled", found.Status)

	missing, err := s.repo.FindByChainAndContract(context.Background(), "eth", "0xother")
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(missing)

	blank, err := s.repo.FindByChainAndContract(context.Background(), "", "0xabc")
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(blank)
}
