package migrations_test

import (
	"testing"

	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/stretchr/testify/assert"
)

func TestChains_Thresholds_NotNullBackfillsAndRejectsNull(t *testing.T) {
	fixtures.TestDB(t)
	migration := &migrations.M00000000000580ChainsThresholdsNotNull{}

	assert.Equal(t, "NO", chainColumnNullable(t, "gas_readiness_threshold_raw"))
	assert.Equal(t, "NO", chainColumnNullable(t, "dust_threshold_native_raw"))
	assert.Equal(t, "NO", chainColumnNullable(t, "dust_threshold_usd"))
	assert.Equal(t, int64(1), constraintCount(t, "chains_dust_threshold_usd_non_negative"))

	require.NoError(t, migration.Down())
	assert.Equal(t, "YES", chainColumnNullable(t, "gas_readiness_threshold_raw"))
	assert.Equal(t, "YES", chainColumnNullable(t, "dust_threshold_usd"))
	assert.Zero(t, constraintCount(t, "chains_gas_readiness_threshold_raw_non_negative"))

	insertBareChain(t, "polygon")
	insertBareChain(t, "eth")
	exec(t, `UPDATE chains SET gas_readiness_threshold_raw = '111' WHERE id = 'eth'`)
	insertBareChain(t, "btc")

	require.NoError(t, migration.Up())

	assert.Equal(t, "500000000000000000", chainText(t, "gas_readiness_threshold_raw", "polygon"))
	assert.Equal(t, "100000000000000000", chainText(t, "dust_threshold_native_raw", "polygon"))
	assert.Equal(t, "0.1000", chainText(t, "dust_threshold_usd", "polygon"))
	assert.Equal(t, "111", chainText(t, "gas_readiness_threshold_raw", "eth"), "an existing threshold is kept")
	assert.Equal(t, "500000000000000", chainText(t, "dust_threshold_native_raw", "eth"))
	assert.Equal(t, "1.0000", chainText(t, "dust_threshold_usd", "eth"))
	assert.Equal(t, "", chainText(t, "gas_readiness_threshold_raw", "btc"))
	assert.Equal(t, "10000", chainText(t, "dust_threshold_native_raw", "btc"))
	assert.Equal(t, "0.0000", chainText(t, "dust_threshold_usd", "btc"))

	_, err := facades.Orm().Query().Exec(
		`INSERT INTO chains (id, name, adapter_type, native_symbol, native_decimals, rpc_url, required_confirmations, gas_readiness_threshold_raw)
		 VALUES ('nilgas', 'nilgas', 'evm', 'eth', 18, 'rpc', 1, NULL)`,
	)
	assert.Error(t, err)
	_, err = facades.Orm().Query().Exec(`UPDATE chains SET dust_threshold_usd = -1 WHERE id = 'eth'`)
	assert.Error(t, err)
	_, err = facades.Orm().Query().Exec(`UPDATE chains SET gas_readiness_threshold_raw = '-1' WHERE id = 'eth'`)
	assert.Error(t, err)

	require.NoError(t, migration.Down())
	assert.Equal(t, "YES", chainColumnNullable(t, "dust_threshold_usd"))
	assert.Equal(t, "500000000000000000", chainText(t, "gas_readiness_threshold_raw", "polygon"), "Down keeps the backfill")
	assert.Equal(t, "0.0000", chainText(t, "dust_threshold_usd", "btc"))

	require.NoError(t, migration.Up())
	assert.Equal(t, "111", chainText(t, "gas_readiness_threshold_raw", "eth"))
	assert.Equal(t, "NO", chainColumnNullable(t, "dust_threshold_native_raw"))
}

func TestChains_Thresholds_NotNullRejectsAnUnknownNullChain(t *testing.T) {
	fixtures.TestDB(t)
	migration := &migrations.M00000000000580ChainsThresholdsNotNull{}
	require.NoError(t, migration.Down())
	insertBareChain(t, "zz")

	err := migration.Up()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zz")
	assert.Equal(t, "YES", chainColumnNullable(t, "gas_readiness_threshold_raw"), "a failed backfill does not lock the columns")
}

func insertBareChain(t *testing.T, id string) {
	t.Helper()
	exec(t, `INSERT INTO chains (id, name, adapter_type, native_symbol, native_decimals, rpc_url, required_confirmations, status)
		VALUES (?, ?, 'evm', 'eth', 18, 'rpc', 1, 'active')`, id, id)
}

func chainColumnNullable(t *testing.T, column string) string {
	t.Helper()
	return scalar[string](t, `SELECT is_nullable FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'chains' AND column_name = ?`, column)
}

func chainText(t *testing.T, column, id string) string {
	t.Helper()
	return scalar[string](t, `SELECT `+column+`::text FROM chains WHERE id = ?`, id)
}
