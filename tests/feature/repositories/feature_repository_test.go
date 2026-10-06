package repositories_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type FeatureRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.FeatureRepository
}

func TestFeatureRepositorySuite(t *testing.T) {
	suite.Run(t, new(FeatureRepositoryTestSuite))
}

func (s *FeatureRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.repo = repositories.NewFeatureRepository(nil)
}

func (s *FeatureRepositoryTestSuite) TestGlobalUpsertIsReadBackAndAMissingKeyIsNotFound() {
	ctx := context.Background()

	enabled, found, err := s.repo.GetGlobal(ctx, features.FlagWithdrawalsEnabled)
	s.Require().NoError(err)
	s.False(found)
	s.False(enabled)

	s.Require().NoError(s.repo.UpsertGlobal(ctx, features.FlagWithdrawalsEnabled, false))
	enabled, found, err = s.repo.GetGlobal(ctx, features.FlagWithdrawalsEnabled)
	s.Require().NoError(err)
	s.True(found)
	s.False(enabled)

	s.Require().NoError(s.repo.UpsertGlobal(ctx, features.FlagWithdrawalsEnabled, true))
	rows, err := s.repo.ListGlobal(ctx)
	s.Require().NoError(err)
	s.Len(rows, 1)
	s.Equal(features.FlagWithdrawalsEnabled, rows[0].Key)
	s.True(rows[0].Enabled)

	accountID := uuid.New()
	accountRows, err := s.repo.ListAccount(ctx, accountID)
	s.Require().NoError(err)
	s.Empty(accountRows)
}
