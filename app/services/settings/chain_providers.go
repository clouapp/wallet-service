package settings

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
)

// The chain provider groups move what only the environment held to the platform:
// the TronGrid key (TRON_API_KEY), the Bitcoin-family fallback providers
// (<PREFIX>_FALLBACK_RPC_URL, _FALLBACK_RPC_API_KEY, _TATUM_DATA_API_URL) and the
// real-time XRP deposit stream (LOCAL_XRP_STREAM_*). Each field no row stores keeps
// its environment value.
const (
	GroupProviderTron = "provider_tron"
	GroupXRPStream    = "xrp_stream"

	groupUTXOFallbackPrefix = "utxo_fallbacks_"

	blockChainProviders = "Chain providers"
	blockUTXOFallbacks  = "Bitcoin-family fallbacks"

	KeyFallbackRPCURLs      = "rpc_urls" // exported for the write check
	keyFallbackAPIKey       = "api_key"
	keyFallbackTatumDataAPI = "tatum_data_api_url"

	keyStreamEnabled        = "enabled"
	keyStreamChain          = "chain"
	KeyStreamURL            = "url" // exported for the write check
	keyStreamAddressRefresh = "address_refresh_seconds"

	minStreamAddressRefreshSeconds = 1
	maxStreamAddressRefreshSeconds = 3600

	// restartNote ends the help of a field the adapters read when the chain
	// registry loads (boot, or a platform chain edit), not at each call.
	restartNote = " Applies when the service restarts or a platform chain edit reloads the chains."
)

// UTXOFallbackChains are the Bitcoin-family records whose fallback providers are a
// platform group each (utxo_fallbacks_<chain>).
var UTXOFallbackChains = []string{models.ChainBTC, models.ChainTBTC, models.ChainLTC, models.ChainTLTC}

// UTXOFallbackGroup names the fallback group of a Bitcoin-family chain.
func UTXOFallbackGroup(chainID string) string { return groupUTXOFallbackPrefix + chainID }

func providerTronGroup() Group {
	group := webhookProviderGroup(GroupProviderTron, "TronGrid", keyProviderAPIKey, "API key")
	group.Block = blockChainProviders
	group.Settings[1].Help = "TronGrid key, sent as TRON-PRO-API-KEY to each TRON chain's primary endpoint. " +
		"Write it to replace the stored one; a blank keeps it. A disabled group or no row keeps TRON_API_KEY. " +
		"Applies to every process within a minute, no restart."
	return group
}

func utxoFallbackGroups() []Group {
	groups := make([]Group, 0, len(UTXOFallbackChains))
	for _, chainID := range UTXOFallbackChains {
		prefix := strings.ToUpper(chainID)
		groups = append(groups, Group{
			Name:             UTXOFallbackGroup(chainID),
			Scope:            ScopePlatform,
			Section:          sectionProviders,
			Block:            blockUTXOFallbacks,
			ViewPermission:   policies.PermProvidersView,
			UpdatePermission: policies.PermProvidersUpdate,
			Settings: []Definition{
				{
					Key:   KeyFallbackRPCURLs,
					Label: "Fallback providers",
					Help: "Comma-separated, tried in order: Esplora REST, bitcoind JSON-RPC (https://), " +
						"electrum+ssl://host:port[?cert_sha256=HEX] or a Tatum gateway (https://*.tatum.io); none turns the " +
						"fallbacks off. Sealed (an URL may carry credentials). Unset keeps " + prefix + "_FALLBACK_RPC_URL, " +
						"else the network's built-in list." + restartNote,
					Type:    TypeString,
					Secret:  true,
					Default: func() any { return "" },
				},
				{
					Key:   keyFallbackAPIKey,
					Label: "Tatum API key",
					Help: "Optional Tatum key (x-api-key, sent to *.tatum.io hosts only); with it the Data API also serves UTXOs. " +
						"Unset keeps " + prefix + "_FALLBACK_RPC_API_KEY." + restartNote,
					Type:    TypeString,
					Secret:  true,
					Default: func() any { return "" },
				},
				{
					Key:   keyFallbackTatumDataAPI,
					Label: "Tatum Data API",
					Help: "Base URL of the Tatum Data API used with the key (https). Unset keeps " + prefix +
						"_TATUM_DATA_API_URL, else https://api.tatum.io." + restartNote,
					Type:    TypeString,
					Default: func() any { return "" },
				},
			},
			Validate: validateUTXOFallback,
		})
	}
	return groups
}

func validateUTXOFallback(effective map[string]string) error {
	raw := strings.TrimSpace(effective[keyFallbackTatumDataAPI])
	if raw == "" {
		return nil
	}
	if !strings.HasPrefix(raw, "https://") || strings.ContainsAny(raw, " \t") {
		return &ValidationError{Fields: map[string][]string{keyFallbackTatumDataAPI: {"must be an https URL"}}}
	}
	return nil
}

func xrpStreamGroup() Group {
	return Group{
		Name:    GroupXRPStream,
		Scope:   ScopePlatform,
		Section: sectionScanning,
		Block:   "XRP stream",
		// url may carry a provider key: providers.view and providers.update gate
		// the group, as the provider credentials (a platform_admins row stands in).
		ViewPermission:   policies.PermProvidersView,
		UpdatePermission: policies.PermProvidersUpdate,
		Settings: []Definition{
			{
				Key:   keyStreamEnabled,
				Label: "Enabled",
				Help: "Whether the local workers keep an XRPL WebSocket subscription to the watched addresses of one XRP chain, " +
					"so a deposit is recorded when its ledger validates. The ledger scan of the chain stays the guarantee. " +
					"Unset keeps LOCAL_XRP_STREAM_ENABLED. A change restarts the stream on the next tick.",
				Type:    TypeBool,
				Default: func() any { return false },
			},
			{
				Key:     keyStreamChain,
				Label:   "Chain",
				Help:    "The XRP chain the stream listens to; it must also be a scanned chain. Unset keeps LOCAL_XRP_STREAM_CHAIN.",
				Type:    TypeString,
				Options: []string{"", models.ChainXRP, models.ChainTXRP},
				Default: func() any { return "" },
			},
			{
				Key:   KeyStreamURL,
				Label: "WebSocket URL",
				Help: "rippled or Clio endpoint of the chain's network (wss://; ws:// on the loopback only). Sealed and never logged " +
					"(it may carry a key). Unset keeps LOCAL_XRP_STREAM_URL.",
				Type:    TypeString,
				Secret:  true,
				Default: func() any { return "" },
			},
			{
				Key:     keyStreamAddressRefresh,
				Label:   "Address refresh",
				Help:    "Seconds between two reads of the watched addresses, from 1 to 3600. 0 keeps LOCAL_XRP_STREAM_ADDRESS_REFRESH_SECONDS.",
				Type:    TypeInt,
				Default: func() any { return 0 },
			},
		},
		Validate: validateXRPStream,
	}
}

func validateXRPStream(effective map[string]string) error {
	fields := map[string][]string{}
	if refresh, ok := blankOrBounded(effective[keyStreamAddressRefresh], 0, maxStreamAddressRefreshSeconds); !ok || refresh < 0 {
		fields[keyStreamAddressRefresh] = []string{fmt.Sprintf("must be between 0 and %d", maxStreamAddressRefreshSeconds)}
	}
	if strings.TrimSpace(effective[keyStreamEnabled]) == "true" && strings.TrimSpace(effective[keyStreamChain]) == "" {
		fields[keyStreamChain] = []string{"must be set when enabled"}
	}
	if len(fields) == 0 {
		return nil
	}
	return &ValidationError{Fields: fields}
}

// TronAPIKey returns the opened TronGrid key when provider_tron is enabled and its
// seal opens, else envKey (TRON_API_KEY). The key is never logged or placed in an error.
func (s *Service) TronAPIKey(ctx context.Context, envKey string) string {
	fallback := strings.TrimSpace(envKey)
	opened, ok := s.openedProviderSecret(ctx, GroupProviderTron, keyProviderAPIKey)
	if !ok {
		return fallback
	}
	return opened
}

// openedProviderSecret opens secretKey of an enabled provider group; ok is false for
// a missing row, a disabled group, a value that is not sealed or does not open, or a
// failed read. The store and sealer errors are dropped so they cannot carry the key.
func (s *Service) openedProviderSecret(ctx context.Context, group, secretKey string) (string, bool) {
	if s == nil || ctx == nil || s.sealer == nil {
		return "", false
	}
	stored, err := s.platformValues(ctx, group)
	if err != nil || len(stored) == 0 || trimmedValue(stored, keyProviderEnabled) != "true" {
		return "", false
	}
	return s.openSealed(stored[secretKey])
}

// openSealed opens one stored secret; ok is false when it is blank, not sealed or
// does not open.
func (s *Service) openSealed(sealed string) (string, bool) {
	if s == nil || s.sealer == nil || !IsSealed(sealed) {
		return "", false
	}
	opened, err := s.sealer.Open(sealed)
	if err != nil || strings.TrimSpace(opened) == "" || IsSealed(opened) {
		return "", false
	}
	return strings.TrimSpace(opened), true
}

// UTXOFallback is one Bitcoin-family chain's fallback providers. A false Use field
// keeps that environment value.
type UTXOFallback struct {
	RPCURLs         string
	APIKey          string
	TatumDataAPIURL string
	UseRPCURLs      bool
	UseAPIKey       bool
	UseTatumDataAPI bool
}

// EffectiveUTXOFallback reads utxo_fallbacks_<chainID>. A missing row leaves every
// Use field false; a secret that does not open is not used (logged by the caller as
// the environment value); a database failure is returned.
func (s *Service) EffectiveUTXOFallback(ctx context.Context, chainID string) (UTXOFallback, error) {
	if s == nil {
		return UTXOFallback{}, errServiceRequired
	}
	if ctx == nil {
		return UTXOFallback{}, fmt.Errorf("utxo fallback settings: context is required")
	}
	stored, err := s.platformValues(ctx, UTXOFallbackGroup(chainID))
	if err != nil {
		return UTXOFallback{}, err
	}
	var out UTXOFallback
	if urls, ok := s.openSealed(stored[KeyFallbackRPCURLs]); ok {
		out.RPCURLs, out.UseRPCURLs = urls, true
	}
	if key, ok := s.openSealed(stored[keyFallbackAPIKey]); ok {
		out.APIKey, out.UseAPIKey = key, true
	}
	if base := trimmedValue(stored, keyFallbackTatumDataAPI); base != "" {
		out.TatumDataAPIURL, out.UseTatumDataAPI = base, true
	}
	return out, nil
}

// XRPStream is the stored xrp_stream row. A false Use field keeps that environment
// value; URL is opened.
type XRPStream struct {
	Enabled               bool
	Chain                 string
	URL                   string
	AddressRefreshSeconds int
	UseEnabled            bool
	UseChain              bool
	UseURL                bool
}

// EffectiveXRPStream reads xrp_stream at the moment of use. A database failure is
// returned so the caller keeps the stream it runs.
func (s *Service) EffectiveXRPStream(ctx context.Context) (XRPStream, error) {
	if s == nil {
		return XRPStream{}, errServiceRequired
	}
	if ctx == nil {
		return XRPStream{}, fmt.Errorf("xrp stream settings: context is required")
	}
	stored, err := s.platformValues(ctx, GroupXRPStream)
	if err != nil {
		return XRPStream{}, err
	}
	var out XRPStream
	switch trimmedValue(stored, keyStreamEnabled) {
	case "true":
		out.Enabled, out.UseEnabled = true, true
	case "false":
		out.UseEnabled = true
	}
	if chainID := trimmedValue(stored, keyStreamChain); chainID != "" {
		out.Chain, out.UseChain = chainID, true
	}
	if url, ok := s.openSealed(stored[KeyStreamURL]); ok {
		out.URL, out.UseURL = url, true
	}
	if refresh, err := strconv.Atoi(trimmedValue(stored, keyStreamAddressRefresh)); err == nil &&
		refresh >= minStreamAddressRefreshSeconds && refresh <= maxStreamAddressRefreshSeconds {
		out.AddressRefreshSeconds = refresh
	}
	return out, nil
}
