package migrations_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func confirmedDepositsIndexes(t *testing.T) int64 {
	t.Helper()
	return countRows(t, `
		SELECT count(*) FROM pg_indexes
		WHERE tablename = 'transactions' AND indexname = 'transactions_confirmed_deposits_index'
		  AND indexdef LIKE '%(confirmed_at)%'
		  AND indexdef LIKE '%tx_type = ''deposit''%'
		  AND indexdef LIKE '%status = ''confirmed''%'`)
}

func TestConfirmed_DepositsIndex_CoversTheDepositEventBackfillAndDropsIt(t *testing.T) {
	fixtures.TestDB(t)
	migration := &migrations.M00000000000620CreateConfirmedDepositsIndex{}
	assert.Equal(t, int64(1), confirmedDepositsIndexes(t))

	require.NoError(t, migration.Down())
	assert.Zero(t, confirmedDepositsIndexes(t))
	require.NoError(t, migration.Up())
	require.NoError(t, migration.Up(), "Up must be repeatable")
	assert.Equal(t, int64(1), confirmedDepositsIndexes(t))
}
