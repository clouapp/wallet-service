package migrations_test

import (
	"testing"

	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func depositSignatureCursorStateColumns(t *testing.T) int64 {
	t.Helper()
	return countRows(t, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'deposit_signature_cursors' AND is_nullable = 'NO'
		  AND column_name IN ('account_state', 'account_state_slot')`)
}

// Migration 700 adds the account state the signature sync last saw each account in,
// empty with slot 0 on every row it finds (no state known: the sync reads those
// accounts in turn as before), and its Down drops them and keeps the rows.
func TestAdd_DepositSignatureCursorStates_AddsEmptyStatesAndDropsThem(t *testing.T) {
	fixtures.TestDB(t)
	migration := &migrations.M00000000000700AddDepositSignatureCursorStates{}
	assert.Equal(t, int64(2), depositSignatureCursorStateColumns(t))

	require.NoError(t, migration.Down())
	assert.Zero(t, depositSignatureCursorStateColumns(t))
	_, err := facades.Orm().Query().Exec(`
		INSERT INTO deposit_signature_cursors (chain, account, signature, slot, created_at, updated_at)
		VALUES ('tsol', 'owner-a', 'sig-top', 200, NOW(), NOW())`)
	require.NoError(t, err, "a row saved before the states")

	require.NoError(t, migration.Up())
	require.NoError(t, migration.Up(), "Up must be repeatable")
	assert.Equal(t, int64(2), depositSignatureCursorStateColumns(t))
	assert.Equal(t, int64(1), countRows(t, `SELECT count(*) FROM deposit_signature_cursors
		WHERE account = 'owner-a' AND signature = 'sig-top' AND account_state = '' AND account_state_slot = 0`),
		"the row keeps its cursor, with no state known")

	require.NoError(t, migration.Down())
	assert.Zero(t, depositSignatureCursorStateColumns(t))
	assert.Equal(t, int64(1), countRows(t, `SELECT count(*) FROM deposit_signature_cursors WHERE account = 'owner-a'`))
	require.NoError(t, migration.Up())
}
