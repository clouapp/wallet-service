package settings

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/services/swap"
	"github.com/stretchr/testify/require"
)

func TestSwapRouting_NetworkCurrencyAndGlobalPrecedence(t *testing.T) {
	store := newMemoryStore()
	service := NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: &memoryCache{}, Activity: &recordingActivity{}})
	store.PutPlatform(GroupSwaps, map[string]string{"enabled": "true", keyPreferredProvider: swap.ProviderSwapKit, keySwapFallback: "true"})
	store.PutPlatform(GroupLiFi, map[string]string{"enabled": "true"})
	store.PutPlatform(GroupSwapKit, map[string]string{"enabled": "true", "api_key": "enc:v1:swapkit-fixture"})
	store.PutPlatform("swap_usdt", map[string]string{keyPreferredProvider: swap.ProviderLiFi, keySwapFallback: "disabled"})
	store.PutPlatform("swap_usdt_tron", map[string]string{keyPreferredProvider: swap.ProviderSwapKit, keySwapFallback: "enabled"})
	ctx := context.Background()
	tron, err := service.SwapRouting(ctx, "USDT", "tron")
	require.NoError(t, err)
	require.Equal(t, swap.ProviderSwapKit, tron.Preferred)
	require.True(t, tron.Fallback)
	eth, err := service.SwapRouting(ctx, "USDT", "eth")
	require.NoError(t, err)
	require.Equal(t, swap.ProviderLiFi, eth.Preferred)
	require.False(t, eth.Fallback)
	btc, err := service.SwapRouting(ctx, "BTC", "btc")
	require.NoError(t, err)
	require.Equal(t, swap.ProviderSwapKit, btc.Preferred)
	require.True(t, btc.Fallback)
	require.Equal(t, 60*time.Second, btc.QuoteTTL)
	require.True(t, btc.Providers[swap.ProviderLiFi].Enabled)
	require.Equal(t, "swapkit-fixture", btc.Providers[swap.ProviderSwapKit].APIKey)
}

func TestSwapSettings_AdminOnlySealedCredentialsAndValidation(t *testing.T) {
	store := newMemoryStore()
	actor := uuid.New()
	service := NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: &memoryCache{}, Activity: &recordingActivity{}}).WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	ctx := context.Background()
	_, err := service.SavePlatform(ctx, uuid.New(), GroupSwapKit, map[string]any{"enabled": true, "api_key": "credential-fixture"})
	require.Error(t, err)
	_, err = service.SavePlatform(ctx, actor, GroupSwapKit, map[string]any{"enabled": true})
	require.Error(t, err)
	view, err := service.SavePlatform(ctx, actor, GroupSwapKit, map[string]any{"enabled": true, "api_key": "credential-fixture"})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(store.rows[platformStoreKey(GroupSwapKit)]["api_key"], "enc:v1:"))
	for _, field := range view.Fields {
		if field.Key == "api_key" {
			require.Nil(t, field.Value)
		}
	}
	_, err = service.SavePlatform(ctx, actor, GroupSwapKit, map[string]any{"enabled": false})
	require.NoError(t, err)
	_, err = service.SavePlatform(ctx, actor, GroupSwaps, map[string]any{keySwapSlippage: 501})
	require.Error(t, err)
	_, err = service.SavePlatform(ctx, actor, GroupSwaps, map[string]any{keySwapTTL: 301})
	require.Error(t, err)
	_, err = service.SavePlatform(ctx, actor, "swap_btc", map[string]any{keyPreferredProvider: "unknown"})
	require.Error(t, err)
}
