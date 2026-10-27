package settings

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
)

func envOf(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func fieldPlan(t *testing.T, groups []EnvImportGroup, group, key string) EnvImportField {
	t.Helper()
	for _, g := range groups {
		if g.Group != group {
			continue
		}
		for _, f := range g.Fields {
			if f.Key == key {
				return f
			}
		}
	}
	t.Fatalf("%s.%s is not in the plan", group, key)
	return EnvImportField{}
}

// TestImport_PlatformEnv_CopiesWhatIsSetSealsSecretsAndKeepsStoredFields: a dry run
// writes nothing; an apply writes what is set through the save (a secret sealed, a
// THROTTLE_* 0 as -1, TRON_API_KEY enabled), leaves a stored field alone unless
// overwrite, and never puts a value in the plan.
func TestImport_PlatformEnv_CopiesWhatIsSetSealsSecretsAndKeepsStoredFields(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	service, actor := newPlatformTestService(store, nil)
	ctx := context.Background()
	env := envOf(map[string]string{
		"LOCAL_DEPOSIT_SCAN_CHAINS":     "eth, sol",
		"TRON_API_KEY":                  "tron-secret",
		"THROTTLE_LOGIN_PER_IP":         "0",
		"THROTTLE_API_PER_MINUTE":       "600",
		"WITHDRAWAL_STALE_AFTER_HOURS":  "6",
		"APP_FRONTEND_URL":              "https://wallet.example",
		"LTC_FALLBACK_RPC_URL":          "none",
		"LOCAL_XRP_STREAM_ENABLED":      "false",
		"DEPOSIT_SCAN_ALERT_LAG_BLOCKS": "0",
	})

	plan, err := service.ImportPlatformEnv(ctx, actor, env, false, false)
	require.NoError(t, err)
	assert.Empty(t, fieldPlan(t, plan, GroupLocalWorkers, KeyDepositScanChains).Skipped)
	assert.Equal(t, "not set", fieldPlan(t, plan, GroupXRPStream, KeyStreamURL).Skipped)
	assert.Equal(t, "keeps the default", fieldPlan(t, plan, groupDepositScan, keyAlertLagBlocks).Skipped)
	assert.True(t, fieldPlan(t, plan, GroupProviderTron, keyProviderAPIKey).Secret)
	assert.Empty(t, fieldPlan(t, plan, GroupProviderTron, keyProviderEnabled).Skipped, "the enabled flag the import sets is in the plan")
	got, err := service.EffectiveLocalWorkers(ctx)
	require.NoError(t, err)
	assert.False(t, got.ScanChainsStored, "a dry run writes nothing")

	_, err = service.ImportPlatformEnv(ctx, actor, env, true, false)
	require.NoError(t, err)
	got, err = service.EffectiveLocalWorkers(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{models.ChainETH, models.ChainSOL}, got.ScanChains)
	assert.Equal(t, "tron-secret", service.TronAPIKey(ctx, ""), "the key is stored sealed and enabled")
	limits, err := service.EffectiveThrottle(ctx)
	require.NoError(t, err)
	assert.Equal(t, ThrottleLimits{KeyThrottleLoginPerIP: -1, KeyThrottleAPIPerMinute: 600}, limits)
	hours, err := service.StaleOutboundHours(ctx)
	require.NoError(t, err)
	assert.Equal(t, 6, hours)
	fallback, err := service.EffectiveUTXOFallback(ctx, models.ChainLTC)
	require.NoError(t, err)
	assert.Equal(t, "none", fallback.RPCURLs)
	stream, err := service.EffectiveXRPStream(ctx)
	require.NoError(t, err)
	assert.True(t, stream.UseEnabled)
	assert.False(t, stream.Enabled)

	changed := envOf(map[string]string{"LOCAL_DEPOSIT_SCAN_CHAINS": "btc"})
	plan, err = service.ImportPlatformEnv(ctx, actor, changed, true, false)
	require.NoError(t, err)
	assert.Equal(t, "already stored", fieldPlan(t, plan, GroupLocalWorkers, KeyDepositScanChains).Skipped)
	_, err = service.ImportPlatformEnv(ctx, actor, changed, true, true)
	require.NoError(t, err)
	got, err = service.EffectiveLocalWorkers(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{models.ChainBTC}, got.ScanChains, "overwrite replaces a stored field")
}

func TestImport_PlatformEnvAs_RefusesAnUnknownActor(t *testing.T) {
	t.Parallel()
	service, _ := newPlatformTestService(newMemoryStore(), nil)
	importer := EnvImportAs{Settings: service, Actors: func(context.Context, string) (uuid.UUID, error) { return uuid.Nil, nil }}
	_, err := importer.ImportPlatformEnvAs(context.Background(), "ghost@example.test", envOf(nil), true, false)
	assert.ErrorIs(t, err, ErrImportActorUnknown)
}
