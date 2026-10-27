package settings

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
)

func newPlatformTestService(store *memoryStore, checks map[string]WriteCheck) (*Service, uuid.UUID) {
	actor := uuid.New()
	service := NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: &memoryCache{}, Activity: &recordingActivity{}, WriteChecks: checks}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	return service, actor
}

func TestEffective_LocalWorkers_ATheStoredListWinsAndAnEmptyOneScansNone(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	service, actor := newPlatformTestService(store, nil)
	ctx := context.Background()

	got, err := service.EffectiveLocalWorkers(ctx)
	require.NoError(t, err)
	assert.False(t, got.ScanChainsStored, "no row: LOCAL_DEPOSIT_SCAN_CHAINS stands")

	_, err = service.SavePlatform(ctx, actor, GroupLocalWorkers, map[string]any{KeyDepositScanChains: []any{models.ChainETH, models.ChainSOL}})
	require.NoError(t, err)
	got, err = service.EffectiveLocalWorkers(ctx)
	require.NoError(t, err)
	assert.Equal(t, LocalWorkerValues{ScanChains: []string{models.ChainETH, models.ChainSOL}, ScanChainsStored: true}, got)

	_, err = service.SavePlatform(ctx, actor, GroupLocalWorkers, map[string]any{KeyDepositScanChains: []any{}})
	require.NoError(t, err)
	got, err = service.EffectiveLocalWorkers(ctx)
	require.NoError(t, err)
	assert.True(t, got.ScanChainsStored)
	assert.Empty(t, got.ScanChains)

	_, err = service.SavePlatform(ctx, actor, GroupLocalWorkers, map[string]any{KeyDepositScanChains: []any{"dogecoin"}})
	var validation *ValidationError
	require.ErrorAs(t, err, &validation, "a chain no scanner reads is refused")
}

func TestEffective_XRPStream_StoredFieldsWinOneByOneAndTheURLIsSealed(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	var checked map[string]string
	service, actor := newPlatformTestService(store, map[string]WriteCheck{
		GroupXRPStream: func(_ context.Context, plain map[string]string) error {
			checked = plain
			if plain[KeyStreamURL] == "ws://internal.example" {
				return &ValidationError{Fields: map[string][]string{KeyStreamURL: {"refused"}}}
			}
			return nil
		},
	})
	ctx := context.Background()

	got, err := service.EffectiveXRPStream(ctx)
	require.NoError(t, err)
	assert.Equal(t, XRPStream{}, got, "no row: every LOCAL_XRP_STREAM_* stands")

	_, err = service.SavePlatform(ctx, actor, GroupXRPStream, map[string]any{keyStreamEnabled: true})
	var validation *ValidationError
	require.ErrorAs(t, err, &validation, "enabled without a chain is refused")
	assert.Contains(t, validation.Fields, keyStreamChain)

	_, err = service.SavePlatform(ctx, actor, GroupXRPStream, map[string]any{KeyStreamURL: "ws://internal.example"})
	require.ErrorAs(t, err, &validation, "the write check refuses before anything is stored")
	assert.Equal(t, "ws://internal.example", checked[KeyStreamURL], "the check reads the plaintext")

	_, err = service.SavePlatform(ctx, actor, GroupXRPStream, map[string]any{
		keyStreamEnabled: true, keyStreamChain: models.ChainTXRP, KeyStreamURL: "wss://s.altnet.rippletest.net:51233", keyStreamAddressRefresh: 30,
	})
	require.NoError(t, err)
	rows, err := store.ListPlatform(ctx, GroupXRPStream)
	require.NoError(t, err)
	stored := valuesFromRows(rows)[KeyStreamURL]
	assert.Equal(t, "enc:v1:wss://s.altnet.rippletest.net:51233", stored, "the URL is stored sealed")

	got, err = service.EffectiveXRPStream(ctx)
	require.NoError(t, err)
	assert.Equal(t, XRPStream{Enabled: true, Chain: models.ChainTXRP, URL: "wss://s.altnet.rippletest.net:51233", AddressRefreshSeconds: 30,
		UseEnabled: true, UseChain: true, UseURL: true}, got)
}

func TestEffective_UTXOFallback_OpensTheSealedListAndKeyAndKeepsTheEnvironmentWithoutThem(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	service, actor := newPlatformTestService(store, nil)
	ctx := context.Background()

	got, err := service.EffectiveUTXOFallback(ctx, models.ChainLTC)
	require.NoError(t, err)
	assert.Equal(t, UTXOFallback{}, got)

	_, err = service.SavePlatform(ctx, actor, UTXOFallbackGroup(models.ChainLTC), map[string]any{keyFallbackTatumDataAPI: "http://api.tatum.io"})
	var validation *ValidationError
	require.ErrorAs(t, err, &validation, "the Data API must be https")

	_, err = service.SavePlatform(ctx, actor, UTXOFallbackGroup(models.ChainLTC), map[string]any{
		KeyFallbackRPCURLs: "https://litecoin-mainnet.gateway.tatum.io", keyFallbackAPIKey: "tatum-key",
	})
	require.NoError(t, err)
	got, err = service.EffectiveUTXOFallback(ctx, models.ChainLTC)
	require.NoError(t, err)
	assert.Equal(t, UTXOFallback{RPCURLs: "https://litecoin-mainnet.gateway.tatum.io", APIKey: "tatum-key", UseRPCURLs: true, UseAPIKey: true}, got)

	other, err := service.EffectiveUTXOFallback(ctx, models.ChainBTC)
	require.NoError(t, err)
	assert.Equal(t, UTXOFallback{}, other, "each chain has its own group")
}

func TestTron_APIKey_IsTheEnabledStoredKeyElseTheEnvironment(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	service, actor := newPlatformTestService(store, nil)
	ctx := context.Background()

	assert.Equal(t, "env-key", service.TronAPIKey(ctx, " env-key "))
	_, err := service.SavePlatform(ctx, actor, GroupProviderTron, map[string]any{keyProviderEnabled: true})
	var validation *ValidationError
	require.ErrorAs(t, err, &validation, "enabled without a key is refused")

	_, err = service.SavePlatform(ctx, actor, GroupProviderTron, map[string]any{keyProviderEnabled: true, keyProviderAPIKey: "panel-key"})
	require.NoError(t, err)
	assert.Equal(t, "panel-key", service.TronAPIKey(ctx, "env-key"))

	_, err = service.SavePlatform(ctx, actor, GroupProviderTron, map[string]any{keyProviderEnabled: false})
	require.NoError(t, err)
	assert.Equal(t, "env-key", service.TronAPIKey(ctx, "env-key"), "a disabled group keeps TRON_API_KEY")
}

func TestEffective_Throttle_KeepsUnsetKeysAndTurnsALimitOffWithMinusOne(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	service, actor := newPlatformTestService(store, nil)
	ctx := context.Background()

	got, err := service.EffectiveThrottle(ctx)
	require.NoError(t, err)
	assert.Empty(t, got)

	_, err = service.SavePlatform(ctx, actor, GroupHTTPThrottle, map[string]any{KeyThrottleLoginPerIP: -2})
	var validation *ValidationError
	require.ErrorAs(t, err, &validation)

	_, err = service.SavePlatform(ctx, actor, GroupHTTPThrottle, map[string]any{KeyThrottleLoginPerIP: 3, KeyThrottleAPIPerMinute: -1, KeyThrottleAuthPerIP: 0})
	require.NoError(t, err)
	got, err = service.EffectiveThrottle(ctx)
	require.NoError(t, err)
	assert.Equal(t, ThrottleLimits{KeyThrottleLoginPerIP: 3, KeyThrottleAPIPerMinute: -1}, got, "0 keeps THROTTLE_AUTH_PER_IP")
}

func TestStale_OutboundHours_AndFrontendURL_AreReadAtTheMomentOfUse(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	service, actor := newPlatformTestService(store, nil)
	ctx := context.Background()

	hours, err := service.StaleOutboundHours(ctx)
	require.NoError(t, err)
	assert.Zero(t, hours)
	_, err = service.SavePlatform(ctx, actor, GroupWithdrawals, map[string]any{keyStaleAfterHours: 12})
	require.NoError(t, err)
	hours, err = service.StaleOutboundHours(ctx)
	require.NoError(t, err)
	assert.Equal(t, 12, hours)

	var validation *ValidationError
	for _, refused := range []string{"wallet.example", "ftp://wallet.example", "https://wallet.example/?a=1", "https://user@wallet.example"} {
		_, err = service.SavePlatform(ctx, actor, GroupPlatformURLs, map[string]any{keyFrontendURL: refused})
		require.ErrorAs(t, err, &validation, refused)
	}
	_, err = service.SavePlatform(ctx, actor, GroupPlatformURLs, map[string]any{keyFrontendURL: "https://wallet.example/app/"})
	require.NoError(t, err)
	base, err := service.FrontendURL(ctx)
	require.NoError(t, err)
	assert.Equal(t, "https://wallet.example/app", base)
}

func TestEffective_DepositScan_ReadsTheAlertThresholds(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	service, actor := newPlatformTestService(store, nil)
	ctx := context.Background()

	_, err := service.SavePlatform(ctx, actor, groupDepositScan, map[string]any{keyAlertLagBlocks: -1})
	var validation *ValidationError
	require.ErrorAs(t, err, &validation)
	_, err = service.SavePlatform(ctx, actor, groupDepositScan, map[string]any{keyAlertLagBlocks: 200, keyAlertStaleMinutes: 15})
	require.NoError(t, err)
	got, err := service.EffectiveDepositScan(ctx)
	require.NoError(t, err)
	assert.Equal(t, 200, got.AlertLagBlocks)
	assert.Equal(t, 15, got.AlertStaleMinutes)
}

func TestSave_MailDelivery_RefusesTheSESDriverUntilItShips(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	service, actor := newPlatformTestService(store, nil)
	_, err := service.SavePlatform(context.Background(), actor, groupMailDelivery, map[string]any{keyMailDriver: "ses"})
	var validation *ValidationError
	require.True(t, errors.As(err, &validation))
	assert.NotContains(t, validation.Fields[keyMailDriver][0], "ses", "ses is not offered")

	store.PutPlatform(groupMailDelivery, map[string]string{keyMailDriver: "ses"})
	delivery, err := service.EffectiveMailDelivery(context.Background())
	require.NoError(t, err)
	assert.False(t, delivery.UseDriver, "a ses stored before keeps MAIL_MAILER")
}
