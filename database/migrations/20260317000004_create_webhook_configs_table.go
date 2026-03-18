package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260317000004CreateWebhookConfigsTable struct {
}

// Signature The unique signature for the migration.
func (r *M20260317000004CreateWebhookConfigsTable) Signature() string {
	return "20260317000004_create_webhook_configs_table"
}

// Up Run the migrations.
func (r *M20260317000004CreateWebhookConfigsTable) Up() error {
	return facades.Schema().Create("webhook_configs", func(table schema.Blueprint) {
		table.Uuid("id").Primary()
		table.String("url", 500).Comment("Webhook endpoint URL")
		table.String("secret", 255).Comment("HMAC secret for webhook signature")
		table.Text("events").Comment("Comma-separated event types (deposit.confirmed, withdrawal.completed, etc.)")
		table.Boolean("is_active").Default(true).Comment("Whether webhook is enabled")
		table.Timestamps()

		// Indexes
		table.Index("is_active")

		table.Comment("Webhook configurations for event notifications")
	})
}

// Down Reverse the migrations.
func (r *M20260317000004CreateWebhookConfigsTable) Down() error {
	return facades.Schema().DropIfExists("webhook_configs")
}
