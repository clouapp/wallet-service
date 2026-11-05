package migrations

import (
	_ "embed"
)

//go:embed sql/ramps.sql
var rampEngineSchema string

// M00000000000720CreateRampIntegrations installs the final schema of the
// ported engine, rather than replaying Xip's historical backfills on Macro.
type M00000000000720CreateRampIntegrations struct{}

func (*M00000000000720CreateRampIntegrations) Signature() string {
	return "00000000000720_create_ramp_integrations"
}

// Up runs inside the migrator's own transaction. It must not open another one: as the
// first pending migration of a batch it would commit the migrator's, leaving the schema
// in place with the migration unrecorded.
func (*M00000000000720CreateRampIntegrations) Up() error {
	return migrationSchema().Sql(rampEngineSchema)
}
func (*M00000000000720CreateRampIntegrations) Down() error {
	return migrationSchema().Sql("DROP TABLE IF EXISTS ramp_tasks, ramp_outbox_events, ramp_customer_features, ramp_customer_actors, ramp_customers; DROP SCHEMA IF EXISTS macro_ramps CASCADE")
}
