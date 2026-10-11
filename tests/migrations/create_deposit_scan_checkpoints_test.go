package migrations_test

import (
	"testing"

	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func depositScanCheckpointColumns(t *testing.T) int64 {
	t.Helper()
	return countRows(t, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'deposit_scan_checkpoints'
		  AND ((column_name = 'chain' AND is_nullable = 'NO')
		    OR (column_name = 'block_number' AND data_type = 'bigint' AND is_nullable = 'NO'))`)
}

func TestCreate_DepositScanCheckpoints_KeepsOneRowPerChainAndDropsTheTable(t *testing.T) {
	fixtures.TestDB(t)
	migration := &migrations.M00000000000610CreateDepositScanCheckpointsTable{}
	assert.Equal(t, int64(2), depositScanCheckpointColumns(t))

	insert := func(chain string, block int64) error {
		_, err := facades.Orm().Query().Exec(`
			INSERT INTO deposit_scan_checkpoints (chain, block_number, created_at, updated_at)
			VALUES (?, ?, NOW(), NOW())`, chain, block)
		return err
	}
	require.NoError(t, insert("eth", 100))
	require.Error(t, insert("eth", 200), "the chain is the primary key")

	require.NoError(t, migration.Down())
	assert.Zero(t, depositScanCheckpointColumns(t))
	require.NoError(t, migration.Up())
	require.NoError(t, migration.Up(), "Up must be repeatable")
	assert.Equal(t, int64(2), depositScanCheckpointColumns(t))
}
