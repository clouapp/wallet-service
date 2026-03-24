package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260324000011AlterTransactionsAddType struct{}

func (r *M20260324000011AlterTransactionsAddType) Signature() string {
	return "20260324000011_alter_transactions_add_type"
}

func (r *M20260324000011AlterTransactionsAddType) Up() error {
	// wallet_id already exists in transactions; just ensure no-op if already present
	// This migration is a placeholder since the field already exists
	return facades.Schema().Table("transactions", func(table schema.Blueprint) {
		// Add type column for filtering (deposit/withdrawal)
		table.String("type", 20).Nullable()
	})
}

func (r *M20260324000011AlterTransactionsAddType) Down() error {
	return facades.Schema().Table("transactions", func(table schema.Blueprint) {
		table.DropColumn("type")
	})
}
