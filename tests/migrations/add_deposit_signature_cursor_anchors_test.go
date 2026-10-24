package migrations_test

import (
	"testing"

	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func depositSignatureCursorAnchorColumns(t *testing.T) int64 {
	t.Helper()
	return countRows(t, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'deposit_signature_cursors' AND is_nullable = 'NO' AND data_type = 'character varying'
		  AND column_name IN ('backfill_anchor', 'ahead_anchor')`)
}

// Migration 690 adds the anchors of the two stretch ends a backfill lists below, empty
// on every row it finds (no anchor known: the sync reads those stretches again from
// their newest signature), and its Down drops them and keeps the rows.
func TestAdd_DepositSignatureCursorAnchors_AddsEmptyAnchorsAndDropsThem(t *testing.T) {
	fixtures.TestDB(t)
	migration := &migrations.M00000000000690AddDepositSignatureCursorAnchors{}
	assert.Equal(t, int64(2), depositSignatureCursorAnchorColumns(t))

	require.NoError(t, migration.Down())
	assert.Zero(t, depositSignatureCursorAnchorColumns(t))
	_, err := facades.Orm().Query().Exec(`
		INSERT INTO deposit_signature_cursors (chain, account, signature, slot, backfill_before, ahead_signature, ahead_before, created_at, updated_at)
		VALUES ('tsol', 'owner-a', 'sig-top', 200, 'sig-150', 'sig-900', 'sig-800', NOW(), NOW())`)
	require.NoError(t, err, "a row saved before the anchors")

	require.NoError(t, migration.Up())
	require.NoError(t, migration.Up(), "Up must be repeatable")
	assert.Equal(t, int64(2), depositSignatureCursorAnchorColumns(t))
	assert.Equal(t, int64(1), countRows(t, `SELECT count(*) FROM deposit_signature_cursors
		WHERE account = 'owner-a' AND backfill_before = 'sig-150' AND backfill_anchor = '' AND ahead_before = 'sig-800' AND ahead_anchor = ''`),
		"the row keeps its stretches, with no anchor known")

	require.NoError(t, migration.Down())
	assert.Zero(t, depositSignatureCursorAnchorColumns(t))
	assert.Equal(t, int64(1), countRows(t, `SELECT count(*) FROM deposit_signature_cursors WHERE account = 'owner-a'`))
	require.NoError(t, migration.Up())
}
