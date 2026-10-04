package migrations_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/mocks"
)

func TestMoveAccountSweepLimitsCopiesTheJSONAndLeavesTheColumn(t *testing.T) {
	mocks.TestDB(t)
	account := mocks.InsertAccount(t, "sweep-json")
	other := mocks.InsertAccount(t, "sweep-other")
	exec(t, `UPDATE accounts SET sweep_limits = CAST(? AS jsonb) WHERE id = ?`,
		`{"max_addresses_per_request":{"evm":40,"sol":8,"btc":12},"max_consolidate_requests_per_day":7,"daily_withdraw_cap_usd":"12.50"}`,
		account.ID)
	exec(t, `UPDATE accounts SET sweep_limits = CAST(? AS jsonb) WHERE id = ?`,
		`{"max_addresses_evm":3}`,
		other.ID)
	exec(t, `INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		VALUES (?, 'account_sweep_limits', 'max_addresses_evm', '9', NOW(), NOW())`, other.ID)

	migration := &migrations.M00000000000520MoveAccountSweepLimitsToSettings{}
	require.NoError(t, migration.Up())
	require.NoError(t, migration.Up())

	require.Equal(t, "40", sweepSetting(t, account.ID, "max_addresses_evm"))
	require.Equal(t, "8", sweepSetting(t, account.ID, "max_addresses_solana"))
	require.Equal(t, "12", sweepSetting(t, account.ID, "max_addresses_bitcoin"))
	require.Equal(t, "7", sweepSetting(t, account.ID, "max_consolidate_requests_per_day"))
	require.Equal(t, "12.50", sweepSetting(t, account.ID, "daily_withdraw_cap_usd"))
	require.Equal(t, int64(1), scalar[int64](t, `SELECT count(*) FROM settings WHERE account_id = ? AND "key" = 'max_addresses_evm'`, account.ID))
	require.Contains(t, scalar[string](t, `SELECT sweep_limits::text FROM accounts WHERE id = ?`, account.ID), "12.50")

	require.Equal(t, "9", sweepSetting(t, other.ID, "max_addresses_evm"))
	require.Equal(t, int64(1), scalar[int64](t, `SELECT count(*) FROM settings WHERE account_id = ?`, other.ID))

	exec(t, `UPDATE settings SET value = '15.00' WHERE account_id = ? AND "key" = 'daily_withdraw_cap_usd'`, account.ID)
	require.NoError(t, migration.Down())

	require.Equal(t, int64(0), scalar[int64](t, `SELECT count(*) FROM settings WHERE account_id = ? AND "key" = 'max_addresses_evm'`, account.ID))
	require.Equal(t, "15.00", sweepSetting(t, account.ID, "daily_withdraw_cap_usd"))
	require.Equal(t, "9", sweepSetting(t, other.ID, "max_addresses_evm"))
	require.Contains(t, scalar[string](t, `SELECT sweep_limits::text FROM accounts WHERE id = ?`, account.ID), "12.50")
}

func TestMoveAccountSweepLimitsRefusesANegativeCap(t *testing.T) {
	mocks.TestDB(t)
	account := mocks.InsertAccount(t, "sweep-negative")
	kept := mocks.InsertAccount(t, "sweep-kept")
	exec(t, `UPDATE accounts SET sweep_limits = CAST(? AS jsonb) WHERE id = ?`,
		`{"max_addresses_per_request":{"evm":4},"daily_withdraw_cap_usd":"-1"}`,
		account.ID)
	exec(t, `UPDATE accounts SET sweep_limits = CAST(? AS jsonb) WHERE id = ?`,
		`{"max_addresses_evm":6}`,
		kept.ID)

	err := (&migrations.M00000000000520MoveAccountSweepLimitsToSettings{}).Up()
	require.Error(t, err)
	require.Contains(t, err.Error(), account.ID.String())
	require.NotContains(t, err.Error(), "-1")
	require.Equal(t, int64(0), scalar[int64](t, `SELECT count(*) FROM settings WHERE "group" = 'account_sweep_limits'`))
}

func TestMoveAccountSweepLimitsSkipsBlankNullAndEmptyDocuments(t *testing.T) {
	mocks.TestDB(t)
	blank := mocks.InsertAccount(t, "sweep-blank")
	empty := mocks.InsertAccount(t, "sweep-empty")
	absent := mocks.InsertAccount(t, "sweep-absent")
	exec(t, `UPDATE accounts SET sweep_limits = CAST(? AS jsonb) WHERE id = ?`,
		`{"daily_withdraw_cap_usd":"","max_addresses_per_request":{"evm":null}}`,
		blank.ID)
	exec(t, `UPDATE accounts SET sweep_limits = '{}'::jsonb WHERE id = ?`, empty.ID)

	require.NoError(t, (&migrations.M00000000000520MoveAccountSweepLimitsToSettings{}).Up())

	require.Equal(t, int64(0), scalar[int64](t, `SELECT count(*) FROM settings WHERE account_id IN (?, ?, ?)`, blank.ID, empty.ID, absent.ID))
}

func TestMoveAccountSweepLimitsRejectsANonPositiveCount(t *testing.T) {
	mocks.TestDB(t)
	account := mocks.InsertAccount(t, "sweep-zero")
	exec(t, `UPDATE accounts SET sweep_limits = CAST(? AS jsonb) WHERE id = ?`,
		`{"max_consolidate_requests_per_day":0}`,
		account.ID)

	err := (&migrations.M00000000000520MoveAccountSweepLimitsToSettings{}).Up()
	require.Error(t, err)
	require.Equal(t, int64(0), scalar[int64](t, `SELECT count(*) FROM settings WHERE account_id = ?`, account.ID))
}

func sweepSetting(t *testing.T, accountID uuid.UUID, key string) string {
	t.Helper()
	return scalar[string](t, `SELECT value FROM settings WHERE account_id = ? AND "group" = 'account_sweep_limits' AND "key" = ?`, accountID, key)
}
