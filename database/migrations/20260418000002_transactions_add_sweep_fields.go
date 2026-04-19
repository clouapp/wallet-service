package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260418000002TransactionsAddSweepFields struct{}

func (r *M20260418000002TransactionsAddSweepFields) Signature() string {
	return "20260418000002_transactions_add_sweep_fields"
}

func (r *M20260418000002TransactionsAddSweepFields) Up() error {
	return facades.Schema().Table("transactions", func(table schema.Blueprint) {
		table.Uuid("parent_transaction_id").Nullable().
			Comment("Withdrawal tx id that triggered this sweep/gas_seed")
		table.String("origin", 24).Nullable().
			Comment("user_request | sweep | gas_seed | manual_consolidation")
		table.Index("parent_transaction_id")
		table.Index("origin")
	})
}

func (r *M20260418000002TransactionsAddSweepFields) Down() error {
	return facades.Schema().Table("transactions", func(table schema.Blueprint) {
		table.DropColumn("parent_transaction_id")
		table.DropColumn("origin")
	})
}
