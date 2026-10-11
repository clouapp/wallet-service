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

func TestChain_Repository_Suite(t *testing.T) {
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

func (s *ChainRepositoryTestSuite) TestCreate_And_FindByID() {
	chain := s.chain("teth", true, "active")
	s.Require().NoError(s.repo.Create(context.Background(), chain))

	found, err := s.repo.FindByID(context.Background(), "teth")
	s.Require().NoError(err)
	s.Equal("teth", found.ID)
	s.True(found.IsTestnet)
}

func (s *ChainRepositoryTestSuite) TestFind_ByID_NotFound() {
	found, err := s.repo.FindByID(context.Background(), "missing")
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *ChainRepositoryTestSuite) TestFind_ByID_Empty() {
	found, err := s.repo.FindByID(context.Background(), "")
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *ChainRepositoryTestSuite) TestFind_Active_AndByTestnet() {
	s.Require().NoError(s.repo.Create(context.Background(), s.chain("eth", false, "active")))
	s.Require().NoError(s.repo.Create(context.Background(), s.chain("teth", true, "active")))
	s.Require().NoError(s.repo.Create(context.Background(), s.chain("gone", false, "disabled")))

	active, err := s.repo.FindActive(context.Background())
	s.Require().NoError(err)
	s.Len(active, 2)

	testnets, err := s.repo.FindByTestnet(context.Background(), true)
	s.Require().NoError(err)
	s.Require().Len(testnets, 1)
	s.Equal("teth", testnets[0].ID)
}

func (s *ChainRepositoryTestSuite) TestCreate_DefaultsTheFinalityToConfirmations() {
	s.Require().NoError(s.repo.Create(context.Background(), s.chain("eth", false, "active")))

	found, err := s.repo.FindByID(context.Background(), "eth")
	s.Require().NoError(err)
	s.Equal(models.FinalityConfirmations, found.Finality)
}

func (s *ChainRepositoryTestSuite) TestUpdate_Thresholds_WritesTheCountAndAFinalityTheAdapterCanRead() {
	ctx := context.Background()
	s.Require().NoError(s.repo.Create(ctx, s.chain("tpolygon", true, "active")))
	finalized := models.FinalityFinalizedTag
	lag := 3
	s.Require().NoError(s.repo.UpdateThresholds(ctx, "tpolygon", models.ChainThresholdWrite{Finality: &finalized, RequiredConfirmations: &lag}))

	found, err := s.repo.FindByID(ctx, "tpolygon")
	s.Require().NoError(err)
	s.Equal(models.FinalityFinalizedTag, found.Finality)
	s.Equal(3, found.RequiredConfirmations)

	solidified := models.FinalitySolidified
	s.Error(s.repo.UpdateThresholds(ctx, "tpolygon", models.ChainThresholdWrite{Finality: &solidified}),
		"an EVM chain cannot read the solidified block")
	found, err = s.repo.FindByID(ctx, "tpolygon")
	s.Require().NoError(err)
	s.Equal(models.FinalityFinalizedTag, found.Finality)
}
