package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260327100002AddLogIndexToTransactions struct{}

func (r *M20260327100002AddLogIndexToTransactions) Signature() string {
	return "20260327100002_add_log_index_to_transactions"
}

func (r *M20260327100002AddLogIndexToTransactions) Up() error {
	return facades.Schema().Table("transactions", func(table schema.Blueprint) {
		table.Integer("log_index").Default(-1)
		table.Index("chain", "tx_hash", "log_index", "tx_type").Name("idx_tx_dedup")
	})
}

func (r *M20260327100002AddLogIndexToTransactions) Down() error {
	return facades.Schema().Table("transactions", func(table schema.Blueprint) {
		table.DropIndex("idx_tx_dedup")
		table.DropColumn("log_index")
	})
}
