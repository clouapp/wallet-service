package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260418000004AccountsAddSweepLimits struct{}

func (r *M20260418000004AccountsAddSweepLimits) Signature() string {
	return "20260418000004_accounts_add_sweep_limits"
}

func (r *M20260418000004AccountsAddSweepLimits) Up() error {
	return facades.Schema().Table("accounts", func(table schema.Blueprint) {
		table.Jsonb("sweep_limits").Nullable().
			Comment("Per-account overrides for sweep rate limits and velocity caps")
	})
}

func (r *M20260418000004AccountsAddSweepLimits) Down() error {
	return facades.Schema().Table("accounts", func(table schema.Blueprint) {
		table.DropColumn("sweep_limits")
	})
}
