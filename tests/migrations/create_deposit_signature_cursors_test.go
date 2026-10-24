package migrations_test

import (
	"testing"

	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func depositSignatureCursorColumns(t *testing.T) int64 {
	t.Helper()
	return countRows(t, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'deposit_signature_cursors' AND is_nullable = 'NO'
		  AND ((column_name IN ('chain', 'account', 'signature', 'backfill_before') AND data_type = 'character varying')
		    OR (column_name IN ('slot', 'synced_from_slot') AND data_type = 'bigint')
		    OR (column_name = 'covered' AND data_type = 'boolean'))`)
}

func TestCreate_DepositSignatureCursors_KeepsOneRowPerChainAndAccountAndDropsTheTable(t *testing.T) {
	fixtures.TestDB(t)
	migration := &migrations.M00000000000680CreateDepositSignatureCursorsTable{}
	assert.Equal(t, int64(7), depositSignatureCursorColumns(t))

	insert := func(chain, account string) error {
		_, err := facades.Orm().Query().Exec(`
			INSERT INTO deposit_signature_cursors (chain, account, created_at, updated_at)
			VALUES (?, ?, NOW(), NOW())`, chain, account)
		return err
	}
	require.NoError(t, insert("tsol", "owner-a"))
	require.NoError(t, insert("tsol", "owner-b"))
	require.NoError(t, insert("sol", "owner-a"))
	require.Error(t, insert("tsol", "owner-a"), "the chain and the account are the primary key")
	assert.Equal(t, int64(1), countRows(t, `SELECT count(*) FROM deposit_signature_cursors
		WHERE chain = 'tsol' AND account = 'owner-a' AND signature = '' AND slot = 0 AND backfill_before = ''
		  AND synced_from_slot = 0 AND NOT covered`),
		"a new row has recorded nothing and is not read down to a floor")

	require.NoError(t, migration.Down())
	assert.Zero(t, depositSignatureCursorColumns(t))
	require.NoError(t, migration.Up())
	require.NoError(t, migration.Up(), "Up must be repeatable")
	assert.Equal(t, int64(7), depositSignatureCursorColumns(t))
}
