package repositories_test

import (
	"context"
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type DepositScanCheckpointRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.DepositScanCheckpointRepository
}

func TestDeposit_Scan_CheckpointRepositorySuite(t *testing.T) {
	suite.Run(t, new(DepositScanCheckpointRepositoryTestSuite))
}

func (s *DepositScanCheckpointRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.repo = repositories.NewDepositScanCheckpointRepository(nil)
}

func (s *DepositScanCheckpointRepositoryTestSuite) TestFind_Of_AChainNeverScannedIsNotFound() {
	_, err := s.repo.Find(context.Background(), models.ChainETH)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
}

func (s *DepositScanCheckpointRepositoryTestSuite) TestAdvance_Creates_TheCheckpointAndMovesItForward() {
	ctx := context.Background()

	stored, err := s.repo.Advance(ctx, models.ChainETH, 100)
	s.Require().NoError(err)
	s.EqualValues(100, stored)

	stored, err = s.repo.Advance(ctx, models.ChainETH, 150)
	s.Require().NoError(err)
	s.EqualValues(150, stored)

	found, err := s.repo.Find(ctx, models.ChainETH)
	s.Require().NoError(err)
	s.EqualValues(150, found)
}

func (s *DepositScanCheckpointRepositoryTestSuite) TestAdvance_Never_MovesTheCheckpointBack() {
	ctx := context.Background()
	_, err := s.repo.Advance(ctx, models.ChainPolygon, 500)
	s.Require().NoError(err)

	stored, err := s.repo.Advance(ctx, models.ChainPolygon, 420)
	s.Require().NoError(err)
	s.EqualValues(500, stored, "a slower scanner must not rewind the checkpoint")

	found, err := s.repo.Find(ctx, models.ChainPolygon)
	s.Require().NoError(err)
	s.EqualValues(500, found)
}

func (s *DepositScanCheckpointRepositoryTestSuite) TestAdvance_Keeps_EachChainApart() {
	ctx := context.Background()
	_, err := s.repo.Advance(ctx, models.ChainETH, 10)
	s.Require().NoError(err)
	_, err = s.repo.Advance(ctx, models.ChainPolygon, 20)
	s.Require().NoError(err)

	eth, err := s.repo.Find(ctx, models.ChainETH)
	s.Require().NoError(err)
	polygon, err := s.repo.Find(ctx, models.ChainPolygon)
	s.Require().NoError(err)
	s.EqualValues(10, eth)
	s.EqualValues(20, polygon)
}

func (s *DepositScanCheckpointRepositoryTestSuite) TestAdvance_Refuses_ABlankChainAndABlockPastBigint() {
	ctx := context.Background()

	_, err := s.repo.Advance(ctx, "  ", 1)
	s.Error(err)
	_, err = s.repo.Advance(ctx, models.ChainETH, math.MaxInt64+1)
	s.Error(err)

	_, err = s.repo.Find(ctx, models.ChainETH)
	s.ErrorIs(err, models.ErrRepositoryNotFound, "a refused advance writes nothing")
}

func (s *DepositScanCheckpointRepositoryTestSuite) TestAdvance_Of_ConcurrentScannersKeepsTheHighestBlock() {
	ctx := context.Background()
	const scanners = 12
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, scanners)
	for i := 1; i <= scanners; i++ {
		wg.Add(1)
		go func(block uint64) {
			defer wg.Done()
			<-start
			_, err := s.repo.Advance(ctx, models.ChainETH, block)
			errs <- err
		}(uint64(1000 + i))
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		s.Require().NoError(err)
	}

	found, err := s.repo.Find(ctx, models.ChainETH)
	s.Require().NoError(err)
	s.EqualValues(1000+scanners, found)
}

func (s *DepositScanCheckpointRepositoryTestSuite) TestSet_Moves_TheCheckpointBackAndCreatesAMissingOne() {
	ctx := context.Background()
	_, err := s.repo.Advance(ctx, models.ChainBTC, 5158070)
	s.Require().NoError(err)

	s.Require().NoError(s.repo.Set(ctx, models.ChainBTC, 156875))
	found, err := s.repo.Find(ctx, models.ChainBTC)
	s.Require().NoError(err)
	s.EqualValues(156875, found, "the operator moves the checkpoint back")

	s.Require().NoError(s.repo.Set(ctx, models.ChainETH, 42))
	found, err = s.repo.Find(ctx, models.ChainETH)
	s.Require().NoError(err)
	s.EqualValues(42, found)
}

func (s *DepositScanCheckpointRepositoryTestSuite) TestSet_Refuses_ABlankChainAndABlockPastBigint() {
	ctx := context.Background()

	s.Error(s.repo.Set(ctx, "  ", 1))
	s.Error(s.repo.Set(ctx, models.ChainETH, math.MaxInt64+1))

	_, err := s.repo.Find(ctx, models.ChainETH)
	s.ErrorIs(err, models.ErrRepositoryNotFound, "a refused set writes nothing")
}

func (s *DepositScanCheckpointRepositoryTestSuite) TestDelete_Removes_OnlyThatChainAndIgnoresAMissingOne() {
	ctx := context.Background()
	_, err := s.repo.Advance(ctx, models.ChainBTC, 5158070)
	s.Require().NoError(err)
	_, err = s.repo.Advance(ctx, models.ChainETH, 10)
	s.Require().NoError(err)

	s.Require().NoError(s.repo.Delete(ctx, models.ChainBTC))
	_, err = s.repo.Find(ctx, models.ChainBTC)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	eth, err := s.repo.Find(ctx, models.ChainETH)
	s.Require().NoError(err)
	s.EqualValues(10, eth)

	s.NoError(s.repo.Delete(ctx, models.ChainBTC), "a chain without a checkpoint is not an error")
	s.Error(s.repo.Delete(ctx, " "))
}
