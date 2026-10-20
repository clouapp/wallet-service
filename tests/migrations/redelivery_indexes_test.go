package migrations_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// redeliveryIndexes counts the two indexes a webhook redelivery reads through: the
// events of one config in a created_at window, and the wallets of one account.
func redeliveryIndexes(t *testing.T) int64 {
	t.Helper()
	return countRows(t, `
		SELECT count(*) FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND ((tablename = 'webhook_events' AND indexname = 'webhook_events_config_created_at_index'
		        AND indexdef LIKE '%(webhook_config_id, created_at)')
		    OR (tablename = 'wallets' AND indexname = 'wallets_account_id_index'
		        AND indexdef LIKE '%(account_id)'))`)
}

func TestRedelivery_Indexes_CoverTheConfigWindowAndTheAccountWallets(t *testing.T) {
	fixtures.TestDB(t)
	migration := &migrations.M00000000000660AddRedeliveryIndexes{}
	assert.Equal(t, int64(2), redeliveryIndexes(t))

	require.NoError(t, migration.Down())
	assert.Zero(t, redeliveryIndexes(t))
	require.NoError(t, migration.Down(), "Down must be repeatable")
	require.NoError(t, migration.Up())
	require.NoError(t, migration.Up(), "Up must be repeatable")
	assert.Equal(t, int64(2), redeliveryIndexes(t))
}
