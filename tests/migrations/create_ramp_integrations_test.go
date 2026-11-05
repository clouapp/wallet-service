package migrations_test

import (
	"errors"
	"testing"

	contractsorm "github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func rampEngineTableCount(t *testing.T) int64 {
	t.Helper()
	return countRows(t, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'macro_ramps'`)
}

// runAsFirstPendingMigration does what the framework's Migrator.runUp does for the
// first migration of a batch: there, the schema's Orm is still the facade's singleton,
// so the outer transaction is the query facades.Orm() answers with. A migration that
// opens its own transaction then commits the outer one, whose Commit fails afterwards.
// Later migrations of a batch see another Orm instance, which is why a fresh database
// never shows this.
func runAsFirstPendingMigration(body func() error) error {
	orm := facades.Orm()
	schemaOrm := facades.Schema().Orm()
	query := orm.Query()
	schemaQuery := schemaOrm.Query()
	defer func() {
		orm.SetQuery(query)
		schemaOrm.SetQuery(schemaQuery)
	}()
	return orm.Transaction(func(tx contractsorm.Query) error {
		orm.SetQuery(tx)
		schemaOrm.SetQuery(tx)
		return body()
	})
}

// Migration 720 is the only one pending when an existing database is upgraded. It has
// to run inside the migrator's transaction: otherwise the schema is committed, the
// migration is not recorded and every later migrate fails.
func TestCreate_RampIntegrations_RunsInsideTheMigratorTransaction(t *testing.T) {
	fixtures.TestDB(t)
	migration := &migrations.M00000000000720CreateRampIntegrations{}
	require.NoError(t, migration.Down())
	t.Cleanup(func() {
		if rampEngineTableCount(t) == 0 {
			require.NoError(t, migration.Up())
		}
	})
	require.Zero(t, rampEngineTableCount(t))

	abort := errors.New("a later statement of the batch failed")
	err := runAsFirstPendingMigration(func() error {
		if err := migration.Up(); err != nil {
			return err
		}
		return abort
	})
	require.ErrorIs(t, err, abort, "the migrator must see why it failed")
	assert.Zero(t, rampEngineTableCount(t), "a failed migration leaves nothing behind")

	require.NoError(t, runAsFirstPendingMigration(migration.Up))
	assert.Positive(t, rampEngineTableCount(t))
}
