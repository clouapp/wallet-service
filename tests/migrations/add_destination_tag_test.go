package migrations_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func destinationTagColumns(t *testing.T) int64 {
	t.Helper()
	return countRows(t, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_name IN ('withdrawals', 'transactions') AND column_name = 'destination_tag'
		  AND data_type = 'bigint' AND is_nullable = 'YES'`)
}

func TestAdd_DestinationTag_AddsANullableUint32ColumnAndDropsIt(t *testing.T) {
	fixtures.TestDB(t)
	migration := &migrations.M00000000000600AddDestinationTag{}
	assert.Equal(t, int64(2), destinationTagColumns(t))

	w := fixtures.InsertWallet(t, "txrp")
	insert := func(tag any) error {
		_, err := facades.Orm().Query().Exec(`
			INSERT INTO withdrawals (id, wallet_id, status, amount, destination_address, destination_tag, created_at, updated_at)
			VALUES (?, ?, 'broadcasting', 1, 'rDestination', ?, NOW(), NOW())`, uuid.New(), w.ID, tag)
		return err
	}
	require.NoError(t, insert(nil))
	require.NoError(t, insert(int64(0)))
	require.NoError(t, insert(int64(4294967295)))
	require.Error(t, insert(int64(4294967296)))
	require.Error(t, insert(int64(-1)))

	require.NoError(t, migration.Down())
	assert.Zero(t, destinationTagColumns(t))
	require.NoError(t, migration.Up())
	require.NoError(t, migration.Up(), "Up must be repeatable")
	assert.Equal(t, int64(2), destinationTagColumns(t))
}
