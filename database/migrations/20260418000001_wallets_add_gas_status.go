package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260418000001WalletsAddGasStatus struct{}

func (r *M20260418000001WalletsAddGasStatus) Signature() string {
	return "20260418000001_wallets_add_gas_status"
}

func (r *M20260418000001WalletsAddGasStatus) Up() error {
	return facades.Schema().Table("wallets", func(table schema.Blueprint) {
		table.String("gas_status", 16).Default("unseeded").Comment("unseeded | seeded | low")
		table.Timestamp("gas_last_checked_at").Nullable()
		table.Integer("sweep_policy_version").Default(1)
		table.Index("gas_status")
	})
}

func (r *M20260418000001WalletsAddGasStatus) Down() error {
	return facades.Schema().Table("wallets", func(table schema.Blueprint) {
		table.DropColumn("gas_status")
		table.DropColumn("gas_last_checked_at")
		table.DropColumn("sweep_policy_version")
	})
}
