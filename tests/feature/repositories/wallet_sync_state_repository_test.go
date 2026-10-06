package repositories_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

type WalletSyncStateRepositoryTestSuite struct {
	suite.Suite
	repo   *repositories.WalletSyncStateRepository
	wallet *models.Wallet
}

func TestWalletSyncStateRepositorySuite(t *testing.T) {
	suite.Run(t, new(WalletSyncStateRepositoryTestSuite))
}

func (s *WalletSyncStateRepositoryTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
	s.repo = repositories.NewWalletSyncStateRepository(nil)
	s.wallet = &models.Wallet{
		ID: uuid.New(), Chain: models.ChainPolygon, Label: "sync state wallet",
		MPCCustomerShare: "deadbeef", MPCShareIV: "cafebabe", MPCShareSalt: "feedface",
		MPCSecretARN: "arn:test", MPCPublicKey: "02abc", MPCCurve: "secp256k1",
	}
	s.Require().NoError(repositories.NewWalletRepository(nil).Create(context.Background(), s.wallet))
}

func (s *WalletSyncStateRepositoryTestSuite) syncedBalancesState(syncedAt time.Time) *models.WalletSyncState {
	return &models.WalletSyncState{
		ID:           uuid.New(),
		WalletID:     s.wallet.ID,
		ChainID:      s.wallet.Chain,
		SyncScope:    string(refresh.RefreshScopeBalances),
		Status:       string(types.SyncStatusSynced),
		LastSyncedAt: &syncedAt,
	}
}

func (s *WalletSyncStateRepositoryTestSuite) findBalancesState() *models.WalletSyncState {
	state, err := s.repo.Find(context.Background(), s.wallet.ID, s.wallet.Chain, string(refresh.RefreshScopeBalances))
	s.Require().NoError(err)
	s.Require().NotNil(state)
	return state
}

func (s *WalletSyncStateRepositoryTestSuite) TestUpsertCreatesStateWithTimestamps() {
	s.Require().NoError(s.repo.Upsert(context.Background(), s.syncedBalancesState(time.Now())))

	state := s.findBalancesState()
	s.NotNil(state.CreatedAt)
	s.NotNil(state.UpdatedAt)
}

func (s *WalletSyncStateRepositoryTestSuite) TestUpsertOfExistingStateKeepsCreatedAt() {
	s.Require().NoError(s.repo.Upsert(context.Background(), s.syncedBalancesState(time.Now().Add(-time.Hour))))
	first := s.findBalancesState()
	s.Require().NotNil(first.CreatedAt)

	secondSync := time.Now()
	s.Require().NoError(s.repo.Upsert(context.Background(), s.syncedBalancesState(secondSync)))

	second := s.findBalancesState()
	s.Equal(first.ID, second.ID)
	s.Require().NotNil(second.CreatedAt)
	s.True(first.CreatedAt.StdTime().Equal(second.CreatedAt.StdTime()), "created_at changed: %v -> %v", first.CreatedAt, second.CreatedAt)
	s.Require().NotNil(second.LastSyncedAt)
	s.WithinDuration(secondSync, *second.LastSyncedAt, time.Second)
}

func (s *WalletSyncStateRepositoryTestSuite) TestUpdateFailureAfterUpsertMarksStateFailed() {
	s.Require().NoError(s.repo.Upsert(context.Background(), s.syncedBalancesState(time.Now())))

	s.Require().NoError(s.repo.UpdateFailure(context.Background(), s.wallet.ID, s.wallet.Chain, string(refresh.RefreshScopeBalances), "rpc unavailable"))

	state := s.findBalancesState()
	s.Equal(string(types.SyncStatusFailed), state.Status)
	s.Require().NotNil(state.LastError)
	s.Equal("rpc unavailable", *state.LastError)
	s.NotNil(state.CreatedAt)
}
