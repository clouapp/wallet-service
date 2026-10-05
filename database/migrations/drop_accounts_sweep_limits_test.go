package migrations_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/settings"
	sweepsvc "github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/mocks"
)

func TestDropAccountsSweepLimitsRemovesTheColumnAndSweepReadsSettings(t *testing.T) {
	mocks.TestDB(t)
	require.Equal(t, int64(0), sweepLimitsColumnCount(t), "migrate leaves accounts.sweep_limits dropped")

	account := mocks.InsertAccount(t, "sweep-from-settings")
	exec(t, `INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		VALUES (?, 'account_sweep_limits', 'max_addresses_evm', '40', NOW(), NOW())`, account.ID)
	exec(t, `INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		VALUES (?, 'account_sweep_limits', 'daily_withdraw_cap_usd', '12.50', NOW(), NOW())`, account.ID)

	limits := loadSweepLimits(t, account)
	require.Equal(t, 40, limits.MaxAddressesPerRequest[models.AdapterTypeEVM])
	require.Equal(t, 25, limits.MaxAddressesPerRequest[models.AdapterTypeSolana])
	require.Equal(t, 100, limits.MaxAddressesPerRequest[models.AdapterTypeBitcoin])
	require.Equal(t, 50, limits.MaxConsolidateReqPerDay)
	require.NotNil(t, limits.DailyWithdrawCapUSD)
	require.True(t, limits.DailyWithdrawCapUSD.Equal(decimal.RequireFromString("12.50")))

	migration := &migrations.M00000000000530DropAccountsSweepLimits{}
	require.NoError(t, migration.Down())
	require.Equal(t, int64(1), sweepLimitsColumnCount(t))
	require.Equal(t, "YES", scalar[string](t, `SELECT is_nullable FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'accounts' AND column_name = 'sweep_limits'`))
	require.Equal(t, "jsonb", scalar[string](t, `SELECT udt_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'accounts' AND column_name = 'sweep_limits'`))
	require.Equal(t, int64(1), scalar[int64](t, `SELECT count(*) FROM accounts WHERE id = ? AND sweep_limits IS NULL`, account.ID))
	require.Equal(t, "12.50", sweepSetting(t, account.ID, "daily_withdraw_cap_usd"))
	require.Equal(t, int64(0), scalar[int64](t, `SELECT count(*) FROM settings WHERE "group" = 'account_sweep_limits' AND value LIKE '-%'`))

	require.NoError(t, migration.Up())
	require.Equal(t, int64(0), sweepLimitsColumnCount(t))
	limits = loadSweepLimits(t, account)
	require.Equal(t, 40, limits.MaxAddressesPerRequest[models.AdapterTypeEVM])
	require.True(t, limits.DailyWithdrawCapUSD.Equal(decimal.RequireFromString("12.50")))
}

func sweepLimitsColumnCount(t *testing.T) int64 {
	t.Helper()
	return scalar[int64](t, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'accounts' AND column_name = 'sweep_limits'`)
}

func loadSweepLimits(t *testing.T, account models.Account) *sweepsvc.Limits {
	t.Helper()
	service := settings.NewService(
		repositories.NewSettingRepository(nil),
		settings.CryptSealer{},
		settings.FacadeCache{},
		repositories.NewAccountActivityRepository(nil),
	)
	sweep := sweepsvc.NewService(sweepsvc.Deps{
		SweepLimits: service.EffectiveSweepLimits,
	})
	limits, err := sweep.LoadLimits(context.Background(), account.ID)
	require.NoError(t, err)
	require.NotNil(t, limits)
	return limits
}
