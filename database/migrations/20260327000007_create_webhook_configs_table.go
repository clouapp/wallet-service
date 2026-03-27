package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260327000007CreateWebhookConfigsTable struct{}

func (r *M20260327000007CreateWebhookConfigsTable) Signature() string {
	return "20260327000007_create_webhook_configs_table"
}

func (r *M20260327000007CreateWebhookConfigsTable) Up() error {
	return facades.Schema().Create("webhook_configs", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.String("url", 500).Comment("Webhook endpoint URL")
		table.String("secret", 255).Comment("HMAC secret for webhook signature")
		table.Text("events").Comment("Comma-separated event types (deposit.confirmed, withdrawal.completed, etc.)")
		table.Boolean("is_active").Default(true).Comment("Whether webhook is enabled")
		table.Uuid("wallet_id").Nullable()
		table.String("type", 50).Nullable()
		table.Timestamps()

		table.Index("is_active")
		table.Index("wallet_id")

		table.Comment("Webhook configurations for event notifications")
	})
}

func (r *M20260327000007CreateWebhookConfigsTable) Down() error {
	return facades.Schema().DropIfExists("webhook_configs")
}
