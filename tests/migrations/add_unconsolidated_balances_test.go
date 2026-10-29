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

func unconsolidatedColumns(t *testing.T) int64 {
	t.Helper()
	return countRows(t, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'wallet_asset_balances' AND is_nullable = 'NO'
		  AND column_name IN ('unconsolidated_raw', 'unconsolidated_display', 'unconsolidated_addresses')`)
}

func TestAdd_UnconsolidatedBalances_StartsAtZeroRefusesANegativeAmountAndDropsIt(t *testing.T) {
	fixtures.TestDBFreshSchema(t)
	migration := &migrations.M00000000000710AddUnconsolidatedBalances{}
	assert.Equal(t, int64(3), unconsolidatedColumns(t))

	_, err := facades.Orm().Query().Exec(`
		INSERT INTO chains (id, name, adapter_type, native_symbol, native_decimals, rpc_url, required_confirmations,
			gas_readiness_threshold_raw, dust_threshold_native_raw, dust_threshold_usd, created_at, updated_at)
		VALUES ('teth', 'Ethereum Sepolia', 'evm', 'ETH', 18, 'enc:v1:primary', 12, '', '', 0, NOW(), NOW())`)
	require.NoError(t, err)
	wallet := fixtures.InsertWallet(t, "teth")
	insert := func(assetKey string, extra string, args ...any) error {
		values := append([]any{uuid.New(), wallet.ID, assetKey}, args...)
		_, err := facades.Orm().Query().Exec(`
			INSERT INTO wallet_asset_balances (id, wallet_id, chain_id, asset_type, asset_symbol, asset_key, decimals, amount_raw, amount_display`+extra+`)
			VALUES (?, ?, 'teth', 'native', 'ETH', ?, 18, '5', '0.000000000000000005'`+placeholders(len(args))+`)`, values...)
		return err
	}
	require.NoError(t, insert("teth", ""), "a row written before the refresh fills the columns")
	assert.Equal(t, int64(1), countRows(t, `
		SELECT count(*) FROM wallet_asset_balances
		WHERE unconsolidated_raw = '0' AND unconsolidated_display = '0' AND unconsolidated_addresses = 0`))
	require.NoError(t, insert("teth:usdc", ", unconsolidated_raw, unconsolidated_display, unconsolidated_addresses", "2200", "0.0022", 1))
	require.Error(t, insert("teth:link", ", unconsolidated_raw", "-1"))
	require.Error(t, insert("teth:link", ", unconsolidated_display", " -0.1"))
	require.Error(t, insert("teth:link", ", unconsolidated_addresses", -1))

	require.NoError(t, migration.Down())
	assert.Zero(t, unconsolidatedColumns(t))
	require.NoError(t, migration.Up())
	require.NoError(t, migration.Up(), "Up must be repeatable")
	assert.Equal(t, int64(3), unconsolidatedColumns(t))
}

func placeholders(n int) string {
	out := ""
	for range n {
		out += ", ?"
	}
	return out
}
