package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
)

// All returns all database migrations
func All() []schema.Migration {
	return []schema.Migration{
		&M20260317000001CreateWalletsTable{},
		&M20260317000002CreateAddressesTable{},
		&M20260317000003CreateTransactionsTable{},
		&M20260317000004CreateWebhookConfigsTable{},
		&M20260317000005CreateWebhookEventsTable{},
	}
}
