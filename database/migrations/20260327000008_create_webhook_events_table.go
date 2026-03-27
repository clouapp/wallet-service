package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260327000008CreateWebhookEventsTable struct{}

func (r *M20260327000008CreateWebhookEventsTable) Signature() string {
	return "20260327000008_create_webhook_events_table"
}

func (r *M20260327000008CreateWebhookEventsTable) Up() error {
	return facades.Schema().Create("webhook_events", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.Uuid("transaction_id").Nullable().Comment("Foreign key to transactions table")
		table.String("event_type", 50).Comment("Event type (deposit.confirmed, withdrawal.completed, etc.)")
		table.Text("payload").Comment("JSON event payload")
		table.String("delivery_url", 500).Comment("Target webhook URL")
		table.String("delivery_status", 20).Default("pending").Comment("pending, delivered, failed")
		table.Integer("attempts").Default(0).Comment("Number of delivery attempts")
		table.Integer("max_attempts").Default(10).Comment("Maximum retry attempts")
		table.Text("last_error").Nullable().Comment("Last delivery error message")
		table.Timestamp("delivered_at").Nullable().Comment("Timestamp of successful delivery")
		table.Timestamps()

		table.Index("transaction_id")
		table.Index("delivery_status")
		table.Index("event_type")
		table.Foreign("transaction_id").References("id").On("transactions")

		table.Comment("Webhook delivery events and retry queue")
	})
}

func (r *M20260327000008CreateWebhookEventsTable) Down() error {
	return facades.Schema().DropIfExists("webhook_events")
}
