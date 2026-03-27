package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260327100001CreateWebhookSubscriptionsTable struct{}

func (r *M20260327100001CreateWebhookSubscriptionsTable) Signature() string {
	return "20260327100001_create_webhook_subscriptions_table"
}

func (r *M20260327100001CreateWebhookSubscriptionsTable) Up() error {
	return facades.Schema().Create("webhook_subscriptions", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.String("chain_id", 20)
		table.String("provider", 20)
		table.String("provider_webhook_id", 255)
		table.Text("webhook_url")
		table.Text("signing_secret")
		table.String("status", 20).Default("active")
		table.String("sync_status", 20).Default("synced")
		table.String("synced_addresses_hash", 64).Nullable()
		table.Timestamp("last_synced_at").Nullable()
		table.Timestamps()

		table.Foreign("chain_id").References("id").On("chains")
		table.Index("chain_id", "provider").Name("idx_ws_chain_provider")
	})
}

func (r *M20260327100001CreateWebhookSubscriptionsTable) Down() error {
	return facades.Schema().DropIfExists("webhook_subscriptions")
}
