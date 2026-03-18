package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260317000005CreateWebhookEventsTable struct {
}

// Signature The unique signature for the migration.
func (r *M20260317000005CreateWebhookEventsTable) Signature() string {
	return "20260317000005_create_webhook_events_table"
}

// Up Run the migrations.
func (r *M20260317000005CreateWebhookEventsTable) Up() error {
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

		// Indexes for common queries
		table.Index("transaction_id")
		table.Index("delivery_status")
		table.Index("event_type")

		// Foreign key
		table.Foreign("transaction_id").References("id").On("transactions")

		table.Comment("Webhook delivery events and retry queue")
	})
}

// Down Reverse the migrations.
func (r *M20260317000005CreateWebhookEventsTable) Down() error {
	return facades.Schema().DropIfExists("webhook_events")
}
