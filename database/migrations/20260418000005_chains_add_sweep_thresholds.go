package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260418000005ChainsAddSweepThresholds struct{}

func (r *M20260418000005ChainsAddSweepThresholds) Signature() string {
	return "20260418000005_chains_add_sweep_thresholds"
}

func (r *M20260418000005ChainsAddSweepThresholds) Up() error {
	return facades.Schema().Table("chains", func(table schema.Blueprint) {
		table.Text("gas_readiness_threshold_raw").Nullable().
			Comment("Min native balance (raw units) on BaseAddress to consider wallet gas-ready")
		table.Text("dust_threshold_native_raw").Nullable().
			Comment("Min native balance on a child to be considered sweepable (raw units)")
		table.Decimal("dust_threshold_usd").Places(4).Total(16).Nullable().
			Comment("Min USD-equivalent for token dust filter")
	})
}

func (r *M20260418000005ChainsAddSweepThresholds) Down() error {
	return facades.Schema().Table("chains", func(table schema.Blueprint) {
		table.DropColumn("gas_readiness_threshold_raw")
		table.DropColumn("dust_threshold_native_raw")
		table.DropColumn("dust_threshold_usd")
	})
}
