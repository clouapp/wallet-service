package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260324000008AlterWalletsAddAccountFields struct{}

func (r *M20260324000008AlterWalletsAddAccountFields) Signature() string {
	return "20260324000008_alter_wallets_add_account_fields"
}

func (r *M20260324000008AlterWalletsAddAccountFields) Up() error {
	return facades.Schema().Table("wallets", func(table schema.Blueprint) {
		table.Uuid("account_id").Nullable()
		table.String("status", 20).Default("active")
		table.Integer("fee_rate_min").Nullable()
		table.Integer("fee_rate_max").Nullable()
		table.Decimal("fee_multiplier").Places(4).Total(8).Nullable()
		table.Integer("required_approvals").Default(1)
		table.Timestamp("frozen_until").Nullable()
	})
}

func (r *M20260324000008AlterWalletsAddAccountFields) Down() error {
	return facades.Schema().Table("wallets", func(table schema.Blueprint) {
		table.DropColumn("account_id", "status", "fee_rate_min", "fee_rate_max", "fee_multiplier", "required_approvals", "frozen_until")
	})
}
