package migrations

import "github.com/goravel/framework/contracts/database/schema"

const (
	// webhookEventsConfigCreatedIndex serves a redelivery's window: the events of one
	// config created in [since, until), oldest first.
	webhookEventsConfigCreatedIndex = "webhook_events_config_created_at_index"
	// walletsAccountIndex serves the account scope of a redelivery (the wallets, and
	// through them the transactions, of the caller's account) and every other
	// per-account wallet lookup.
	walletsAccountIndex = "wallets_account_id_index"
)

// M00000000000660AddRedeliveryIndexes indexes what POST /api/v1/webhooks/{id}/redeliver
// reads: webhook_events (webhook_config_id, created_at) and wallets (account_id).
// Neither existed; the redelivery query scanned every event of the table and every
// wallet. Both are plain indexes, built with the table locked for writes while they
// are built (webhook_events is the larger table: seconds per million rows).
type M00000000000660AddRedeliveryIndexes struct{}

func (r *M00000000000660AddRedeliveryIndexes) Signature() string {
	return "00000000000660_add_redelivery_indexes"
}

func (r *M00000000000660AddRedeliveryIndexes) Up() error {
	builder := migrationSchema()
	if !builder.HasIndex("webhook_events", webhookEventsConfigCreatedIndex) {
		if err := builder.Table("webhook_events", func(table schema.Blueprint) {
			table.Index("webhook_config_id", "created_at").Name(webhookEventsConfigCreatedIndex)
		}); err != nil {
			return err
		}
	}
	if !builder.HasIndex("wallets", walletsAccountIndex) {
		return builder.Table("wallets", func(table schema.Blueprint) {
			table.Index("account_id").Name(walletsAccountIndex)
		})
	}
	return nil
}

func (r *M00000000000660AddRedeliveryIndexes) Down() error {
	builder := migrationSchema()
	if builder.HasIndex("wallets", walletsAccountIndex) {
		if err := builder.Table("wallets", func(table schema.Blueprint) {
			table.DropIndexByName(walletsAccountIndex)
		}); err != nil {
			return err
		}
	}
	if builder.HasIndex("webhook_events", webhookEventsConfigCreatedIndex) {
		return builder.Table("webhook_events", func(table schema.Blueprint) {
			table.DropIndexByName(webhookEventsConfigCreatedIndex)
		})
	}
	return nil
}
