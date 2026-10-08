package providers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"

	"github.com/goravel/framework/contracts/foundation"

	bitcoinchain "github.com/macrowallets/waas/app/adapters/chain/bitcoin"
	evmchain "github.com/macrowallets/waas/app/adapters/chain/evm"
	solanachain "github.com/macrowallets/waas/app/adapters/chain/solana"
	tronchain "github.com/macrowallets/waas/app/adapters/chain/tron"
	xrpchain "github.com/macrowallets/waas/app/adapters/chain/xrp"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/pkg/types"
)

// errActiveChainsNotLoaded means Boot has not read the chain catalog yet.
var errActiveChainsNotLoaded = errors.New("active chains were not loaded during boot")

// openActiveChainEndpoint opens a sealed rpc_url. The container factory uses
// the process cipher. Tests replace this with a stub that never logs a URL.
var openActiveChainEndpoint = openChainEndpoint

// openChainEndpoint opens a sealed rpc_url and returns the URL to dial.
// A read failure does not include the URL.
func openChainEndpoint(stored string) (string, error) {
	cipher := facades.Crypt()
	if cipher == nil {
		return "", fmt.Errorf("open chain rpc")
	}
	opened, err := settings.OpenStored(cipher, stored)
	if err != nil {
		return "", fmt.Errorf("open chain rpc")
	}
	endpoint, err := models.DialEndpoint(opened)
	if err != nil {
		return "", fmt.Errorf("open chain rpc")
	}
	return endpoint, nil
}

// chainCatalog is the chain rows the registry service reads. Register only
// stores this adapter; the query runs in ChainRegistryService.Refresh.
type chainCatalog struct {
	repo *repositories.ChainRepository
}

func (c chainCatalog) FindActive(ctx context.Context) ([]models.Chain, error) {
	if c.repo == nil {
		return nil, errActiveChainsNotLoaded
	}
	return c.repo.FindActive(ctx)
}

// refreshChainRegistry loads the sealed catalog and builds the chain registry.
// A read failure is logged and leaves the process up. The URL is not logged.
func refreshChainRegistry(app foundation.Application) {
	if app == nil {
		slog.Error("failed to load chains from DB", "error", errActiveChainsNotLoaded)
		return
	}
	svc, err := resolve[*chainregistry.ChainRegistryService](app)
	if err != nil || svc == nil {
		if err == nil {
			err = errActiveChainsNotLoaded
		}
		slog.Error("failed to load chains from DB", "error", err)
		return
	}
	tokens, _ := bootedActiveTokens()
	if err := svc.Refresh(context.Background(), registerActiveTokens(nil, tokens)); err != nil {
		slog.Error("failed to load chains from DB", "error", err)
	}
}

// registerActiveChains installs the Boot catalog on the chain registry and
// returns the same chains' network names for the block-height providers.
// A chain whose rpc_url does not open is skipped. The URL is not logged.
func registerActiveChains(reg *chainpkg.Registry, rows []models.Chain, tokensByChain map[string][]types.Token) map[string]string {
	networkByChain := make(map[string]string)
	for _, ch := range rows {
		rpcURL, openErr := openActiveChainEndpoint(ch.RpcURL)
		if openErr != nil {
			slog.Warn("failed to open chain rpc, skipping chain", "chain", ch.ID)
			continue
		}
		networkByChain[ch.ID] = ch.ResolveNetwork(rpcURL).Name
		var adapter types.Chain
		switch ch.AdapterType {
		case models.AdapterTypeEVM:
			networkID := int64(0)
			if ch.NetworkID != nil {
				networkID = *ch.NetworkID
			}
			adapter = evmchain.NewEVMLive(evmchain.EVMConfig{
				ChainIDStr:            ch.ID,
				ChainName:             ch.Name,
				NativeSymbol:          ch.NativeSymbol,
				NativeDecimal:         uint8(ch.NativeDecimals),
				NetworkID:             networkID,
				RPCURL:                rpcURL,
				Confirmations:         uint64(ch.RequiredConfirmations),
				ERC20Tokens:           tokensByChain[ch.ID],
				GasReadinessThreshold: resolveGasReadinessThreshold(&ch),
				DustThresholdNative:   resolveDustThresholdNative(&ch),
				StrictLogScan:         !lenientLogScanChains[ch.ID],
			})
		case models.AdapterTypeBitcoin:
			network := "mainnet"
			if ch.IsTestnet {
				network = "testnet"
			}
			btcCfg := bitcoinchain.BitcoinConfig{
				ChainIDStr:    ch.ID,
				ChainName:     ch.Name,
				NativeSymbol:  ch.NativeSymbol,
				NativeDecimal: uint8(ch.NativeDecimals),
				RPCURL:        rpcURL,
				Network:       network,
				IsTestnet:     ch.IsTestnet,
				Confirmations: uint64(ch.RequiredConfirmations),
			}
			fallbackKey := "vault.utxo_fallbacks." + ch.ID
			tatumKey := facades.Config().GetString(fallbackKey + ".api_key")
			btcCfg.Fallbacks = bitcoinFallbacks(
				facades.Config().GetString(fallbackKey+".rpc_urls"),
				tatumKey,
				bitcoinchain.DefaultBitcoinFallbackURLs(btcCfg),
			)
			btcCfg.TatumDataAPIURL = facades.Config().GetString(fallbackKey + ".tatum_data_api_url")
			slog.Info("btc fallback providers", "chain", ch.ID, "count", len(btcCfg.Fallbacks), "tatum_key_set", tatumKey != "")
			adapter = bitcoinchain.NewBitcoinLive(btcCfg)
		case models.AdapterTypeXRP:
			adapter = xrpchain.NewLive(xrpchain.Config{
				ChainIDStr:            ch.ID,
				ChainName:             ch.Name,
				NativeSymbol:          ch.NativeSymbol,
				RPCURL:                rpcURL,
				IsTestnet:             ch.IsTestnet,
				Confirmations:         uint64(ch.RequiredConfirmations),
				GasReadinessThreshold: resolveGasReadinessThreshold(&ch),
				DustThresholdNative:   resolveDustThresholdNative(&ch),
			})
		case models.AdapterTypeTron:
			adapter = tronchain.NewTronLive(tronchain.TronConfig{
				ChainIDStr:            ch.ID,
				ChainName:             ch.Name,
				NativeSymbol:          ch.NativeSymbol,
				RPCURL:                rpcURL,
				APIKey:                facades.Config().GetString("vault.tron.api_key"),
				IsTestnet:             ch.IsTestnet,
				Confirmations:         uint64(ch.RequiredConfirmations),
				Tokens:                tokensByChain[ch.ID],
				GasReadinessThreshold: resolveGasReadinessThreshold(&ch),
				DustThresholdNative:   resolveDustThresholdNative(&ch),
			})
		case models.AdapterTypeSolana:
			adapter = solanachain.NewSolanaLive(solanachain.SolanaConfig{
				ChainIDStr:    ch.ID,
				ChainName:     ch.Name,
				NativeSymbol:  ch.NativeSymbol,
				NativeDecimal: uint8(ch.NativeDecimals),
				RPCURL:        rpcURL,
				Confirmations: uint64(ch.RequiredConfirmations),
			})
		default:
			slog.Warn("unknown adapter type, skipping", "chain", ch.ID, "adapter", ch.AdapterType)
			continue
		}
		if reg != nil {
			reg.RegisterChain(adapter)
		}
	}
	return networkByChain
}

// noBitcoinFallbacks as <PREFIX>_FALLBACK_RPC_URL turns the built-in list off.
const noBitcoinFallbacks = "none"

// bitcoinFallbacks are the secondary providers of a Bitcoin-family chain: the
// comma-separated configured URLs, the network's built-in defaults when none is
// configured, or nothing for "none". apiKey is the optional Tatum key; the adapter
// sends it to Tatum hosts only.
func bitcoinFallbacks(configured, apiKey string, defaults []string) []bitcoinchain.BitcoinFallback {
	if strings.EqualFold(strings.TrimSpace(configured), noBitcoinFallbacks) {
		return nil
	}
	var urls []string
	for _, rawURL := range strings.Split(configured, ",") {
		if rawURL = strings.TrimSpace(rawURL); rawURL != "" {
			urls = append(urls, rawURL)
		}
	}
	if len(urls) == 0 {
		urls = defaults
	}
	fallbacks := make([]bitcoinchain.BitcoinFallback, 0, len(urls))
	for _, rawURL := range urls {
		fallbacks = append(fallbacks, bitcoinchain.BitcoinFallback{URL: rawURL, APIKey: apiKey})
	}
	return fallbacks
}

// lenientLogScanChains keep their deployed deposit scan: a block whose eth_getLogs
// fails is scanned for native transfers only. Every other EVM record fails the block
// so the scanner retries it (evmchain.EVMConfig.StrictLogScan).
var lenientLogScanChains = map[string]bool{
	models.ChainETH:      true,
	models.ChainTETH:     true,
	models.ChainPolygon:  true,
	models.ChainTPolygon: true,
}

// resolveGasReadinessThreshold is the chains.gas_readiness_threshold_raw value.
// An empty column means the chain has no gas threshold. Environment variables
// do not fill it in.
func resolveGasReadinessThreshold(ch *models.Chain) *big.Int {
	if ch == nil {
		return nil
	}
	return ch.GasReadinessThreshold()
}

// resolveDustThresholdNative is the chains.dust_threshold_native_raw value.
// An empty column means no native dust filter. Environment variables do not
// fill it in.
func resolveDustThresholdNative(ch *models.Chain) *big.Int {
	if ch == nil {
		return nil
	}
	return ch.DustThresholdNative()
}
