package repositories_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type ChainRPCEndpointRepositoryTestSuite struct {
	suite.Suite
	repo   *repositories.ChainRPCEndpointRepository
	chains *repositories.ChainRepository
}

func TestChain_RPCEndpoint_RepositorySuite(t *testing.T) {
	suite.Run(t, new(ChainRPCEndpointRepositoryTestSuite))
}

func (s *ChainRPCEndpointRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.repo = repositories.NewChainRPCEndpointRepository(nil)
	s.chains = repositories.NewChainRepository(nil)
	for _, id := range []string{models.ChainETH, models.ChainSOL} {
		s.Require().NoError(s.chains.Create(context.Background(), &models.Chain{
			ID: id, Name: id, AdapterType: models.AdapterTypeEVM, NativeSymbol: "ETH",
			NativeDecimals: 18, RpcURL: "enc:v1:primary", RequiredConfirmations: 1, Status: "active",
		}))
	}
}

func sealedFallback(position int, enabled bool) models.ChainRPCEndpoint {
	return models.ChainRPCEndpoint{URL: fmt.Sprintf("enc:v1:fallback-%d", position), Position: position, Enabled: enabled}
}

func (s *ChainRPCEndpointRepositoryTestSuite) TestList_ByChain_OfAChainWithoutFallbacksIsEmpty() {
	rows, err := s.repo.ListByChain(context.Background(), models.ChainETH)
	s.Require().NoError(err)
	s.Empty(rows)
}

func (s *ChainRPCEndpointRepositoryTestSuite) TestReplace_Writes_TheWholeListInPositionOrder() {
	ctx := context.Background()
	s.Require().NoError(s.repo.Replace(ctx, models.ChainETH, []models.ChainRPCEndpoint{
		sealedFallback(3, false), sealedFallback(2, true), sealedFallback(4, true),
	}))

	rows, err := s.repo.ListByChain(ctx, models.ChainETH)
	s.Require().NoError(err)
	s.Require().Len(rows, 3)
	for i, row := range rows {
		s.Equal(i+2, row.Position)
		s.Equal(models.ChainETH, row.ChainID)
		s.NotZero(row.ID)
		s.False(row.CreatedAt.IsZero())
	}
	s.False(rows[1].Enabled, "position 3 was written disabled")

	s.Require().NoError(s.repo.Replace(ctx, models.ChainETH, []models.ChainRPCEndpoint{sealedFallback(2, true)}))
	rows, err = s.repo.ListByChain(ctx, models.ChainETH)
	s.Require().NoError(err)
	s.Len(rows, 1, "a replaced list keeps only the new rows")

	s.Require().NoError(s.repo.Replace(ctx, models.ChainETH, nil))
	rows, err = s.repo.ListByChain(ctx, models.ChainETH)
	s.Require().NoError(err)
	s.Empty(rows, "an empty list removes every fallback")
}

func (s *ChainRPCEndpointRepositoryTestSuite) TestReplace_Keeps_EachChainApart() {
	ctx := context.Background()
	s.Require().NoError(s.repo.Replace(ctx, models.ChainETH, []models.ChainRPCEndpoint{sealedFallback(2, true)}))
	s.Require().NoError(s.repo.Replace(ctx, models.ChainSOL, []models.ChainRPCEndpoint{sealedFallback(2, true), sealedFallback(3, true)}))
	s.Require().NoError(s.repo.Replace(ctx, models.ChainETH, []models.ChainRPCEndpoint{sealedFallback(2, false)}))

	sol, err := s.repo.ListByChain(ctx, models.ChainSOL)
	s.Require().NoError(err)
	s.Len(sol, 2)
}

func (s *ChainRPCEndpointRepositoryTestSuite) TestReplace_Refuses_AnUnsealedURLAPositionOfThePrimaryAndARepeat() {
	ctx := context.Background()
	s.Require().NoError(s.repo.Replace(ctx, models.ChainETH, []models.ChainRPCEndpoint{sealedFallback(2, true)}))

	bad := map[string][]models.ChainRPCEndpoint{
		"plaintext url": {{URL: "https://sepolia.drpc.org", Position: 2, Enabled: true}},
		"primary slot":  {{URL: "enc:v1:x", Position: models.ChainRPCPrimaryPosition, Enabled: true}},
		"repeat":        {sealedFallback(2, true), sealedFallback(2, true)},
	}
	for name, rows := range bad {
		s.Error(s.repo.Replace(ctx, models.ChainETH, rows), name)
		kept, err := s.repo.ListByChain(ctx, models.ChainETH)
		s.Require().NoError(err)
		s.Len(kept, 1, "%s: a refused list must leave the stored one", name)
	}
	s.Error(s.repo.Replace(ctx, " ", nil))
}

func (s *ChainRPCEndpointRepositoryTestSuite) TestList_Enabled_ReturnsOnlyEnabledRowsOfEveryChain() {
	ctx := context.Background()
	s.Require().NoError(s.repo.Replace(ctx, models.ChainSOL, []models.ChainRPCEndpoint{sealedFallback(2, true)}))
	s.Require().NoError(s.repo.Replace(ctx, models.ChainETH, []models.ChainRPCEndpoint{
		sealedFallback(2, true), sealedFallback(3, false), sealedFallback(4, true),
	}))

	rows, err := s.repo.ListEnabled(ctx)
	s.Require().NoError(err)
	s.Require().Len(rows, 3)
	s.Equal([]string{models.ChainETH, models.ChainETH, models.ChainSOL}, []string{rows[0].ChainID, rows[1].ChainID, rows[2].ChainID})
	s.Equal([]int{2, 4, 2}, []int{rows[0].Position, rows[1].Position, rows[2].Position})
}

func (s *ChainRPCEndpointRepositoryTestSuite) TestWithin_Rolls_BackThePrimaryAndTheFallbacksTogether() {
	ctx := context.Background()
	failure := errors.New("verification failed after the write")
	err := s.repo.Within(ctx, func(ctx context.Context) error {
		if err := s.chains.UpdateRPCURL(ctx, models.ChainETH, "enc:v1:new-primary"); err != nil {
			return err
		}
		if err := s.repo.Replace(ctx, models.ChainETH, []models.ChainRPCEndpoint{sealedFallback(2, true)}); err != nil {
			return err
		}
		return failure
	})
	s.ErrorIs(err, failure)

	chain, err := s.chains.FindByID(ctx, models.ChainETH)
	s.Require().NoError(err)
	s.Equal("enc:v1:primary", chain.RpcURL)
	rows, err := s.repo.ListByChain(ctx, models.ChainETH)
	s.Require().NoError(err)
	s.Empty(rows)
}

func (s *ChainRPCEndpointRepositoryTestSuite) TestDeleting_AChainDeletesItsFallbacks() {
	ctx := context.Background()
	s.Require().NoError(s.repo.Replace(ctx, models.ChainSOL, []models.ChainRPCEndpoint{sealedFallback(2, true)}))
	_, err := facades.Orm().Query().Exec(`DELETE FROM chains WHERE id = ?`, models.ChainSOL)
	s.Require().NoError(err)

	rows, err := s.repo.ListByChain(ctx, models.ChainSOL)
	s.Require().NoError(err)
	s.Empty(rows)
}

func (s *ChainRPCEndpointRepositoryTestSuite) TestDisable_All_TurnsOffTheEnabledFallbacksOfOneChain() {
	ctx := context.Background()
	s.Require().NoError(s.repo.Replace(ctx, models.ChainETH, []models.ChainRPCEndpoint{
		sealedFallback(2, true), sealedFallback(3, false), sealedFallback(4, true),
	}))
	s.Require().NoError(s.repo.Replace(ctx, models.ChainSOL, []models.ChainRPCEndpoint{sealedFallback(2, true)}))

	disabled, err := s.repo.DisableAll(ctx, models.ChainETH)
	s.Require().NoError(err)
	s.Equal([]int{2, 4}, disabled)
	rows, err := s.repo.ListEnabled(ctx)
	s.Require().NoError(err)
	s.Require().Len(rows, 1)
	s.Equal(models.ChainSOL, rows[0].ChainID)

	again, err := s.repo.DisableAll(ctx, models.ChainETH)
	s.Require().NoError(err)
	s.Empty(again)
}

func (s *ChainRPCEndpointRepositoryTestSuite) TestDisable_Positions_TurnsOffOnlyThoseOfThatChain() {
	ctx := context.Background()
	s.Require().NoError(s.repo.Replace(ctx, models.ChainETH, []models.ChainRPCEndpoint{
		sealedFallback(2, true), sealedFallback(3, true), sealedFallback(4, true),
	}))
	s.Require().NoError(s.repo.Replace(ctx, models.ChainSOL, []models.ChainRPCEndpoint{sealedFallback(3, true)}))

	s.Require().NoError(s.repo.DisablePositions(ctx, models.ChainETH, []int{2, 4, 9}))
	s.Require().NoError(s.repo.DisablePositions(ctx, models.ChainETH, nil))
	rows, err := s.repo.ListEnabled(ctx)
	s.Require().NoError(err)
	s.Require().Len(rows, 2)
	s.Equal([]int{3, 3}, []int{rows[0].Position, rows[1].Position})
	s.Equal([]string{models.ChainETH, models.ChainSOL}, []string{rows[0].ChainID, rows[1].ChainID})
}

func (s *ChainRPCEndpointRepositoryTestSuite) TestLock_ByID_ReadsTheChainInsideTheTransaction() {
	ctx := context.Background()
	err := s.repo.Within(ctx, func(ctx context.Context) error {
		locked, err := s.chains.LockByID(ctx, models.ChainETH)
		s.Require().NoError(err)
		s.Equal("enc:v1:primary", locked.RpcURL)
		_, err = s.chains.LockByID(ctx, "missing")
		s.ErrorIs(err, models.ErrRepositoryNotFound)
		return nil
	})
	s.Require().NoError(err)
}
