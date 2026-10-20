package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/pkg/pgerr"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func activeSubscriptionIndexes(t *testing.T) int64 {
	t.Helper()
	return countRows(t, `
		SELECT count(*) FROM pg_indexes
		WHERE tablename = 'webhook_subscriptions' AND indexname = 'webhook_subscriptions_active_chain_provider_unique'
		  AND indexdef LIKE 'CREATE UNIQUE INDEX%'
		  AND indexdef LIKE '%(chain_id, provider)%'
		  AND indexdef LIKE '%status%''active''%'`)
}

func insertSubscription(t *testing.T, provider, status string) error {
	t.Helper()
	return facades.Orm().Query().Create(&models.WebhookSubscription{
		ID: uuid.New(), ChainID: "eth", Provider: provider, ProviderWebhookID: "wh_" + uuid.NewString()[:8],
		WebhookURL: "https://wallets.example.com/v1/webhooks/ingest/" + provider + "/eth", SigningSecret: "enc:v1:sealed",
		Status: status, SyncStatus: "synced",
	})
}

func TestUnique_ActiveSubscription_OneActiveSubscriptionPerChainAndProvider(t *testing.T) {
	fixtures.TestDB(t)
	require.NoError(t, repositories.NewChainRepository(nil).Create(context.Background(), &models.Chain{
		ID: "eth", Name: "eth", AdapterType: models.AdapterTypeEVM, NativeSymbol: "ETH", NativeDecimals: 18,
		RpcURL: "enc", RequiredConfirmations: 1, IsTestnet: true, Status: "active", DisplayOrder: 1,
	}))
	assert.Equal(t, int64(1), activeSubscriptionIndexes(t))

	require.NoError(t, insertSubscription(t, "alchemy", "active"))
	require.NoError(t, insertSubscription(t, "alchemy", "disabled"), "a subscription that no longer pushes may stay")
	require.NoError(t, insertSubscription(t, "alchemy", "disabled"))
	require.NoError(t, insertSubscription(t, "helius", "active"), "another provider is another subscription")
	err := insertSubscription(t, "alchemy", "active")
	assert.True(t, pgerr.IsUniqueViolation(err), "a second active alchemy subscription of eth: %v", err)
}

func TestUnique_ActiveSubscription_DownDropsTheIndexAndUpRefusesDuplicates(t *testing.T) {
	fixtures.TestDB(t)
	require.NoError(t, repositories.NewChainRepository(nil).Create(context.Background(), &models.Chain{
		ID: "eth", Name: "eth", AdapterType: models.AdapterTypeEVM, NativeSymbol: "ETH", NativeDecimals: 18,
		RpcURL: "enc", RequiredConfirmations: 1, IsTestnet: true, Status: "active", DisplayOrder: 1,
	}))
	migration := &migrations.M00000000000670UniqueActiveWebhookSubscription{}

	require.NoError(t, migration.Down())
	assert.Zero(t, activeSubscriptionIndexes(t))
	require.NoError(t, insertSubscription(t, "alchemy", "active"))
	require.NoError(t, insertSubscription(t, "alchemy", "active"))

	err := migration.Up()
	require.Error(t, err, "two active subscriptions must be resolved before the index exists")
	assert.Contains(t, err.Error(), "1 (chain_id, provider) active subscription groups")
	assert.Zero(t, activeSubscriptionIndexes(t))

	_, err = facades.Orm().Query().Exec(`UPDATE webhook_subscriptions SET status = 'disabled' WHERE id IN (SELECT id FROM webhook_subscriptions LIMIT 1)`)
	require.NoError(t, err)
	require.NoError(t, migration.Up())
	require.NoError(t, migration.Up(), "Up must be repeatable")
	assert.Equal(t, int64(1), activeSubscriptionIndexes(t))
}
