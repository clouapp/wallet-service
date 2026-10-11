package migrations_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func updatedAtKeysetIndexes(t *testing.T) int64 {
	t.Helper()
	return countRows(t, `
		SELECT count(*) FROM pg_indexes
		WHERE schemaname = current_schema() AND tablename = 'transactions'
		  AND indexname = 'transactions_updated_at_id_index'
		  AND indexdef LIKE '%(updated_at, id)'`)
}

func insertTransactionStampedAt(t *testing.T, walletID uuid.UUID, createdAt, updatedAt any) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, `
		INSERT INTO transactions (id, wallet_id, external_user_id, chain, tx_type, to_address, amount, asset, status, created_at, updated_at)
		VALUES (?, ?, 'user', 'eth', 'deposit', '0xto', '1', 'eth', 'pending', ?, ?)`,
		id, walletID, createdAt, updatedAt)
	return id
}

func TestTransactions_UpdatedAtIndex_CoversTheChangesFeedAndBackfillsNullStamps(t *testing.T) {
	fixtures.TestDB(t)
	migration := &migrations.M00000000000630AddTransactionsUpdatedAtIndex{}
	assert.Equal(t, int64(1), updatedAtKeysetIndexes(t))

	wallet := fixtures.InsertWallet(t, "eth")
	created := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	stamped := time.Date(2025, 6, 7, 8, 9, 10, 0, time.UTC)
	unstamped := insertTransactionStampedAt(t, wallet.ID, created, nil)
	neverStamped := insertTransactionStampedAt(t, wallet.ID, nil, nil)
	kept := insertTransactionStampedAt(t, wallet.ID, created, stamped)

	require.NoError(t, migration.Down())
	assert.Zero(t, updatedAtKeysetIndexes(t))
	before := time.Now().UTC().Add(-time.Minute)
	require.NoError(t, migration.Up())
	require.NoError(t, migration.Up(), "Up must be repeatable")
	assert.Equal(t, int64(1), updatedAtKeysetIndexes(t))

	updatedAt := func(id uuid.UUID) time.Time {
		return scalar[time.Time](t, `SELECT updated_at FROM transactions WHERE id = ?`, id).UTC()
	}
	assert.Equal(t, created, updatedAt(unstamped), "a row without updated_at takes its created_at")
	assert.True(t, updatedAt(neverStamped).After(before), "a row without either stamp takes the migration time")
	assert.Equal(t, stamped, updatedAt(kept), "a stamped row keeps its updated_at")
	assert.Zero(t, countRows(t, `SELECT count(*) FROM transactions WHERE updated_at IS NULL`))
}
