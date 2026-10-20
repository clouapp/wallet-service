package repositories_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type WebhookSubscriptionRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.WebhookSubscriptionRepository
}

func TestWebhook_Subscription_RepositorySuite(t *testing.T) {
	suite.Run(t, new(WebhookSubscriptionRepositoryTestSuite))
}

func (s *WebhookSubscriptionRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.repo = repositories.NewWebhookSubscriptionRepository(nil)
	chain := &models.Chain{
		ID: "eth", Name: "eth", AdapterType: models.AdapterTypeEVM, NativeSymbol: "ETH",
		NativeDecimals: 18, RpcURL: "enc", RequiredConfirmations: 1,
		IsTestnet: true, Status: "active", DisplayOrder: 1,
	}
	s.Require().NoError(repositories.NewChainRepository(nil).Create(context.Background(), chain))
}

func (s *WebhookSubscriptionRepositoryTestSuite) subscription(provider, chainID, status string) *models.WebhookSubscription {
	return &models.WebhookSubscription{
		ID:                uuid.New(),
		ChainID:           chainID,
		Provider:          provider,
		ProviderWebhookID: "wh_" + provider,
		WebhookURL:        "https://example.test/ingest",
		SigningSecret:     "sec",
		Status:            status,
		SyncStatus:        "synced",
	}
}

func (s *WebhookSubscriptionRepositoryTestSuite) TestFind_ByChainID_NotFound() {
	found, err := s.repo.FindByChainID(context.Background(), "eth")
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *WebhookSubscriptionRepositoryTestSuite) TestFind_ByChainID_Empty() {
	found, err := s.repo.FindByChainID(context.Background(), "")
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *WebhookSubscriptionRepositoryTestSuite) TestCreate_Find_AndRecordSync() {
	active := s.subscription("alchemy", "eth", "active")
	s.Require().NoError(s.repo.Create(context.Background(), active))
	inactive := s.subscription("helius", "eth", "disabled")
	s.Require().NoError(s.repo.Create(context.Background(), inactive))

	found, err := s.repo.FindByChainID(context.Background(), "eth")
	s.Require().NoError(err)
	s.Equal(active.ID, found.ID)
	s.Equal("sec", found.SigningSecret)

	byProvider, err := s.repo.FindByProviderAndChain(context.Background(), "helius", "eth")
	s.Require().NoError(err)
	s.Equal(inactive.ID, byProvider.ID)

	missing, err := s.repo.FindByProviderAndChain(context.Background(), "quicknode", "eth")
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(missing)

	activeOnly, err := s.repo.FindAllActive(context.Background())
	s.Require().NoError(err)
	s.Len(activeOnly, 1)

	s.Require().NoError(s.repo.SetSyncStatus(context.Background(), active.ID, "syncing"))
	syncedAt := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	s.Require().NoError(s.repo.RecordSync(context.Background(), active.ID, "synced", "abc", syncedAt))

	again, err := s.repo.FindByChainID(context.Background(), "eth")
	s.Require().NoError(err)
	s.Equal("synced", again.SyncStatus)
	s.Require().NotNil(again.SyncedAddressesHash)
	s.Equal("abc", *again.SyncedAddressesHash)
}

func (s *WebhookSubscriptionRepositoryTestSuite) TestFind_All_ListsEverySubscriptionByChainAndProvider() {
	ctx := context.Background()
	sol := &models.Chain{
		ID: "sol", Name: "sol", AdapterType: models.AdapterTypeSolana, NativeSymbol: "SOL",
		NativeDecimals: 9, RpcURL: "enc", RequiredConfirmations: 1, Status: "active", DisplayOrder: 2,
	}
	s.Require().NoError(repositories.NewChainRepository(nil).Create(ctx, sol))
	helius := s.subscription("helius", "sol", "active")
	quicknode := s.subscription("quicknode", "eth", "disabled")
	alchemy := s.subscription("alchemy", "eth", "active")
	for _, sub := range []*models.WebhookSubscription{helius, quicknode, alchemy} {
		s.Require().NoError(s.repo.Create(ctx, sub))
	}

	all, err := s.repo.FindAll(ctx)
	s.Require().NoError(err)

	s.Require().Len(all, 3, "inactive subscriptions are listed too")
	s.Equal([]uuid.UUID{alchemy.ID, quicknode.ID, helius.ID}, []uuid.UUID{all[0].ID, all[1].ID, all[2].ID})
}
