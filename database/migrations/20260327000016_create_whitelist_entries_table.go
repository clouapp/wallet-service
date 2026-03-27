package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260327000016CreateWhitelistEntriesTable struct{}

func (r *M20260327000016CreateWhitelistEntriesTable) Signature() string {
	return "20260327000016_create_whitelist_entries_table"
}

func (r *M20260327000016CreateWhitelistEntriesTable) Up() error {
	return facades.Schema().Create("whitelist_entries", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.Uuid("wallet_id")
		table.String("label", 255).Nullable()
		table.Text("address")
		table.Timestamps()
		table.Foreign("wallet_id").References("id").On("wallets").CascadeOnDelete()
		table.Index("wallet_id")
	})
}

func (r *M20260327000016CreateWhitelistEntriesTable) Down() error {
	return facades.Schema().DropIfExists("whitelist_entries")
}
