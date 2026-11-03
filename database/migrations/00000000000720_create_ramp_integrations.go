package migrations

import (
	_ "embed"

	"github.com/goravel/framework/contracts/database/orm"
)

//go:embed sql/ramps.sql
var rampEngineSchema string

// M00000000000720CreateRampIntegrations installs the final schema of the
// ported engine, rather than replaying Xip's historical backfills on Macro.
type M00000000000720CreateRampIntegrations struct{}

func (*M00000000000720CreateRampIntegrations) Signature() string {
	return "00000000000720_create_ramp_integrations"
}
func (*M00000000000720CreateRampIntegrations) Up() error {
	return migrationTransaction(func(tx orm.Query) error { _, err := tx.Exec(rampEngineSchema); return err })
}
func (*M00000000000720CreateRampIntegrations) Down() error {
	return migrationSchema().Sql("DROP TABLE IF EXISTS ramp_tasks, ramp_outbox_events, ramp_customer_features, ramp_customer_actors, ramp_customers; DROP SCHEMA IF EXISTS macro_ramps CASCADE")
}
