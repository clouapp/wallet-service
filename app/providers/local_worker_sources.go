package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	xrpchain "github.com/macrowallets/waas/app/adapters/chain/xrp"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainsync"
	"github.com/macrowallets/waas/app/services/localworkers"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/xrpstream"
)

// localWorkerSettings is the part of the platform settings the local workers read
// on every supervisor tick.
type localWorkerSettings interface {
	EffectiveLocalWorkers(ctx context.Context) (settings.LocalWorkerValues, error)
	EffectiveXRPStream(ctx context.Context) (settings.XRPStream, error)
}

// scanChainSettings is the scanned chain list: the local_workers setting when a row
// stores it (an empty list scans none), else LOCAL_DEPOSIT_SCAN_CHAINS.
type scanChainSettings struct {
	stored localWorkerSettings
	env    string
}

// LocalScanChains is the list the local workers scan, read at each tick.
func LocalScanChains(stored *settings.Service) localworkers.ScanChainSource {
	return scanChainSettings{stored: stored, env: facades.Config().GetString("vault.local_workers.deposit_scan_chains")}
}

// PushedChains is the chains with an active ingest subscription, once each, in
// chain order: the chains a provider pushes deposits for.
func PushedChains(subscriptions interface {
	FindAllActive(ctx context.Context) ([]models.WebhookSubscription, error)
}) localworkers.PushedChainSource {
	return pushedChains{subscriptions: subscriptions}
}

type pushedChains struct {
	subscriptions interface {
		FindAllActive(ctx context.Context) ([]models.WebhookSubscription, error)
	}
}

func (p pushedChains) PushedChains(ctx context.Context) ([]string, error) {
	subs, err := p.subscriptions.FindAllActive(ctx)
	if err != nil {
		return nil, err
	}
	chains := make([]string, 0, len(subs))
	for _, sub := range subs {
		if !slices.Contains(chains, sub.ChainID) {
			chains = append(chains, sub.ChainID)
		}
	}
	slices.Sort(chains)
	return chains, nil
}

func (s scanChainSettings) ScanChains(ctx context.Context) ([]string, error) {
	values, err := s.stored.EffectiveLocalWorkers(ctx)
	if err != nil {
		return nil, err
	}
	if values.ScanChainsStored {
		return values.ScanChains, nil
	}
	return parseChainList(s.env), nil
}

// xrpStreamConfig is one resolved stream: each xrp_stream field a row stores over
// its LOCAL_XRP_STREAM_* value.
type xrpStreamConfig struct {
	enabled bool
	chainID string
	url     string
	refresh time.Duration
}

func (c xrpStreamConfig) key() string {
	if !c.enabled {
		return ""
	}
	sum := sha256.Sum256([]byte(c.chainID + "\x00" + c.url + "\x00" + c.refresh.String()))
	return hex.EncodeToString(sum[:8])
}

// xrpDepositStreams is the real-time XRP deposit stream the settings ask for.
type xrpDepositStreams struct {
	stored    localWorkerSettings
	registry  *chainpkg.Registry
	addresses *repositories.AddressRepository
	deposits  xrpstream.DepositRecorder

	mu      sync.Mutex
	resolve map[string]xrpStreamConfig
}

// XRPDepositStreams opens the XRP deposit stream xrp_stream (else LOCAL_XRP_STREAM_*)
// asks for, over the RPC guard, recording through deposits.
func XRPDepositStreams(stored *settings.Service, registry *chainpkg.Registry, addresses *repositories.AddressRepository, deposits xrpstream.DepositRecorder) localworkers.DepositStreams {
	return &xrpDepositStreams{stored: stored, registry: registry, addresses: addresses, deposits: deposits, resolve: map[string]xrpStreamConfig{}}
}

func (s *xrpDepositStreams) config(ctx context.Context) (xrpStreamConfig, error) {
	cfg := xrpStreamConfig{
		enabled: facades.Config().GetBool("vault.local_workers.xrp_stream_enabled"),
		chainID: strings.TrimSpace(facades.Config().GetString("vault.local_workers.xrp_stream_chain")),
		url:     strings.TrimSpace(facades.Config().GetString("vault.local_workers.xrp_stream_url")),
		refresh: time.Duration(facades.Config().GetInt("vault.local_workers.xrp_stream_address_refresh_seconds")) * time.Second,
	}
	stored, err := s.stored.EffectiveXRPStream(ctx)
	if err != nil {
		return xrpStreamConfig{}, err
	}
	if stored.UseEnabled {
		cfg.enabled = stored.Enabled
	}
	if stored.UseChain {
		cfg.chainID = stored.Chain
	}
	if stored.UseURL {
		cfg.url = stored.URL
	}
	if stored.AddressRefreshSeconds > 0 {
		cfg.refresh = time.Duration(stored.AddressRefreshSeconds) * time.Second
	}
	return cfg, nil
}

func (s *xrpDepositStreams) StreamSpec(ctx context.Context) (localworkers.DepositStreamSpec, error) {
	cfg, err := s.config(ctx)
	if err != nil {
		return localworkers.DepositStreamSpec{}, err
	}
	key := cfg.key()
	if key == "" {
		return localworkers.DepositStreamSpec{}, nil
	}
	s.mu.Lock()
	s.resolve = map[string]xrpStreamConfig{key: cfg}
	s.mu.Unlock()
	return localworkers.DepositStreamSpec{Key: key, Chain: cfg.chainID}, nil
}

// OpenStream builds the stream of spec: the chain must be an XRP chain of the
// registry and the URL set. The URL is never logged or placed in an error.
func (s *xrpDepositStreams) OpenStream(_ context.Context, spec localworkers.DepositStreamSpec) (localworkers.DepositStream, error) {
	s.mu.Lock()
	cfg, ok := s.resolve[spec.Key]
	s.mu.Unlock()
	if !ok {
		return nil, errors.New("xrp stream settings changed while the stream was opened")
	}
	adapter, err := s.registry.Chain(cfg.chainID)
	if err != nil {
		return nil, err
	}
	live, isXRP := adapter.(*xrpchain.Live)
	if !isXRP {
		return nil, fmt.Errorf("xrp stream chain %s is not an XRP chain", cfg.chainID)
	}
	if cfg.url == "" {
		return nil, errors.New("the xrp stream URL is empty (xrp_stream.url or LOCAL_XRP_STREAM_URL)")
	}
	return xrpstream.NewService(xrpstream.Deps{
		ChainID: cfg.chainID,
		// Every session connects through the RPC guard, as the RPC nodes do.
		Dialer: xrpchain.NewStreamDialer(xrpchain.StreamConfig{
			ChainIDStr: cfg.chainID, NativeSymbol: adapter.NativeAsset(), URL: cfg.url, IsTestnet: live.IsTestnet(),
		}, RPCDestinations().DialContext),
		Addresses:      s.addresses,
		Deposits:       s.deposits,
		AddressRefresh: cfg.refresh,
	}), nil
}

// KnownScanChain reports whether registry holds chainID.
func KnownScanChain(registry *chainpkg.Registry) func(chainID string) bool {
	return func(chainID string) bool {
		_, err := registry.Chain(chainID)
		return err == nil
	}
}

// chainEndpointLister reads every active chain's endpoint list as the registry
// builds it: the opened primary and the enabled fallbacks it can check.
func chainEndpointLister(catalog chainCatalog) chainsync.EndpointLister {
	return func(ctx context.Context) (map[string]chainsync.EndpointList, error) {
		rows, err := catalog.FindActive(ctx)
		if err != nil {
			return nil, err
		}
		lists := make(map[string]chainsync.EndpointList, len(rows))
		for _, ch := range rows {
			primary, err := openActiveChainEndpoint(ch.RpcURL)
			if err != nil {
				continue
			}
			list := chainsync.EndpointList{Primary: primary}
			if models.RPCFallbacksSupported(ch.AdapterType) {
				list.Fallbacks = openChainFallbacks(ch, ch.ResolveNetwork(primary).Name)
			}
			slices.SortFunc(list.Fallbacks, func(a, b chainpkg.RPCFallback) int { return a.Position - b.Position })
			lists[ch.ID] = list
		}
		return lists, nil
	}
}

// chainSyncInterval is LOCAL_CHAIN_SYNC_INTERVAL_SECONDS, at least one second.
func chainSyncInterval() time.Duration {
	seconds := facades.Config().GetInt("vault.local_workers.chain_sync_interval_seconds")
	if seconds < 1 {
		return chainsync.DefaultInterval
	}
	return time.Duration(seconds) * time.Second
}
