package migrations_test

import (
	"testing"

	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func chainRPCEndpointsTables(t *testing.T) int64 {
	t.Helper()
	return countRows(t, `SELECT count(*) FROM information_schema.tables WHERE table_name = 'chain_rpc_endpoints'`)
}

func TestCreate_ChainRPCEndpoints_RefusesAPlaintextURLAndThePrimarySlotAndDropsTheTable(t *testing.T) {
	fixtures.TestDB(t)
	migration := &migrations.M00000000000650CreateChainRPCEndpointsTable{}
	require.Equal(t, int64(1), chainRPCEndpointsTables(t))
	_, err := facades.Orm().Query().Exec(`
		INSERT INTO chains (id, name, adapter_type, native_symbol, native_decimals, rpc_url, required_confirmations,
			gas_readiness_threshold_raw, dust_threshold_native_raw, dust_threshold_usd, created_at, updated_at)
		VALUES ('eth', 'Ethereum', 'evm', 'ETH', 18, 'enc:v1:primary', 12, '', '', 0, NOW(), NOW())`)
	require.NoError(t, err)

	insert := func(url string, position int) error {
		_, err := facades.Orm().Query().Exec(`
			INSERT INTO chain_rpc_endpoints (chain_id, url, position) VALUES ('eth', ?, ?)`, url, position)
		return err
	}
	require.NoError(t, insert("enc:v1:fallback", 2))
	assert.Error(t, insert("enc:v1:other", 2), "one endpoint per position")
	assert.Error(t, insert("enc:v1:other", 1), "position 1 is chains.rpc_url")
	assert.Error(t, insert("https://sepolia.drpc.org", 3), "a URL is stored sealed")
	assert.Equal(t, int64(1), countRows(t, `SELECT count(*) FROM chain_rpc_endpoints WHERE enabled`))

	require.NoError(t, migration.Down())
	assert.Zero(t, chainRPCEndpointsTables(t))
	require.NoError(t, migration.Up())
	require.NoError(t, migration.Up(), "Up must be repeatable")
	assert.Equal(t, int64(1), chainRPCEndpointsTables(t))
}
