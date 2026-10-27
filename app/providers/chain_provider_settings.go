package providers

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	bitcoinchain "github.com/macrowallets/waas/app/adapters/chain/bitcoin"
	xrpchain "github.com/macrowallets/waas/app/adapters/chain/xrp"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/pkg/egress"
	"github.com/macrowallets/waas/pkg/types"
)

// chainProviderReadTimeout bounds the settings reads of one registry build.
const chainProviderReadTimeout = 5 * time.Second

// chainProviderSettings is what the chain registry reads from the platform settings
// when it builds the adapters: the TronGrid key and the Bitcoin-family fallbacks.
type chainProviderSettings interface {
	TronAPIKey(ctx context.Context, envKey string) string
	EffectiveUTXOFallback(ctx context.Context, chainID string) (settings.UTXOFallback, error)
}

// activeChainInstaller is registerActiveChains over the platform settings: each
// build reads provider_tron and utxo_fallbacks_<chain>, each field none stores
// keeping its environment value.
func activeChainInstaller(stored chainProviderSettings) chainregistry.CatalogInstaller {
	return func(reg *chainpkg.Registry, rows []models.Chain, tokensByChain map[string][]types.Token) map[string]string {
		return installActiveChains(reg, rows, tokensByChain, stored)
	}
}

// tronAPIKey is the TronGrid key of a registry build: provider_tron when enabled,
// else TRON_API_KEY.
func tronAPIKey(stored chainProviderSettings) string {
	envKey := facades.Config().GetString("vault.tron.api_key")
	if stored == nil {
		return envKey
	}
	ctx, cancel := context.WithTimeout(context.Background(), chainProviderReadTimeout)
	defer cancel()
	return stored.TronAPIKey(ctx, envKey)
}

// utxoFallbackConfig is one Bitcoin-family chain's fallback list, key and Data API:
// each field utxo_fallbacks_<chain> stores wins over <PREFIX>_FALLBACK_RPC_URL,
// _FALLBACK_RPC_API_KEY and _TATUM_DATA_API_URL. A failed read keeps the environment.
func utxoFallbackConfig(stored chainProviderSettings, chainID string) (urls, apiKey, dataAPI string) {
	fallbackKey := "vault.utxo_fallbacks." + chainID
	urls = facades.Config().GetString(fallbackKey + ".rpc_urls")
	apiKey = facades.Config().GetString(fallbackKey + ".api_key")
	dataAPI = facades.Config().GetString(fallbackKey + ".tatum_data_api_url")
	if stored == nil {
		return urls, apiKey, dataAPI
	}
	ctx, cancel := context.WithTimeout(context.Background(), chainProviderReadTimeout)
	defer cancel()
	fallback, err := stored.EffectiveUTXOFallback(ctx, chainID)
	if err != nil {
		slog.Warn("utxo fallback settings unread; keeping the environment values", "chain", chainID)
		return urls, apiKey, dataAPI
	}
	if fallback.UseRPCURLs {
		urls = fallback.RPCURLs
	}
	if fallback.UseAPIKey {
		apiKey = fallback.APIKey
	}
	if fallback.UseTatumDataAPI {
		dataAPI = fallback.TatumDataAPIURL
	}
	return urls, apiKey, dataAPI
}

// chainProviderWriteChecks refuse, before they are sealed, the Bitcoin-family
// fallback URLs and the XRP stream URL the adapters would refuse, or whose host the
// RPC destination guard refuses (a link-local or metadata address).
func chainProviderWriteChecks(destinations *egress.Guard) map[string]settings.WriteCheck {
	checks := map[string]settings.WriteCheck{
		settings.GroupXRPStream: func(ctx context.Context, plain map[string]string) error {
			raw, ok := plain[settings.KeyStreamURL]
			if !ok {
				return nil
			}
			raw = strings.TrimSpace(raw)
			if err := xrpchain.CheckStreamURL(raw); err != nil {
				return fieldRefusal(settings.KeyStreamURL, err.Error())
			}
			dial := "https://" + strings.TrimPrefix(raw, "wss://")
			if strings.HasPrefix(raw, "ws://") {
				dial = "http://" + strings.TrimPrefix(raw, "ws://")
			}
			return destinationRefusal(ctx, destinations, settings.KeyStreamURL, dial)
		},
	}
	utxo := func(ctx context.Context, plain map[string]string) error {
		raw, ok := plain[settings.KeyFallbackRPCURLs]
		if !ok || strings.EqualFold(strings.TrimSpace(raw), noBitcoinFallbacks) {
			return nil
		}
		listed := 0
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			listed++
			if err := bitcoinchain.CheckFallbackURL(part); err != nil {
				return fieldRefusal(settings.KeyFallbackRPCURLs, err.Error())
			}
			if err := destinationRefusal(ctx, destinations, settings.KeyFallbackRPCURLs, fallbackDialURL(part)); err != nil {
				return err
			}
		}
		if listed == 0 {
			return fieldRefusal(settings.KeyFallbackRPCURLs, "list at least one provider, or none")
		}
		return nil
	}
	for _, chainID := range settings.UTXOFallbackChains {
		checks[settings.UTXOFallbackGroup(chainID)] = utxo
	}
	return checks
}

// fallbackDialURL is the http(s) form of a fallback URL the destination guard reads:
// an Electrum server's host and port over https (tcp: http).
func fallbackDialURL(raw string) string {
	switch {
	case strings.HasPrefix(raw, "electrum+ssl://"):
		return "https://" + strings.TrimPrefix(raw, "electrum+ssl://")
	case strings.HasPrefix(raw, "electrum+tcp://"):
		return "http://" + strings.TrimPrefix(raw, "electrum+tcp://")
	}
	return raw
}

func destinationRefusal(ctx context.Context, destinations *egress.Guard, key, dial string) error {
	if destinations == nil {
		return nil
	}
	err := destinations.CheckDestination(ctx, dial)
	if err == nil {
		return nil
	}
	message := "the endpoint is not an allowed destination"
	var refusal *egress.Refusal
	if errors.As(err, &refusal) && refusal.Reason != "" {
		message = refusal.Reason
	}
	return fieldRefusal(key, message)
}

func fieldRefusal(key, message string) error {
	return &settings.ValidationError{Fields: map[string][]string{key: {message}}}
}
