package providers

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/goravel/framework/contracts/foundation"

	bitcoinchain "github.com/macrowallets/waas/app/adapters/chain/bitcoin"
	evmchain "github.com/macrowallets/waas/app/adapters/chain/evm"
	solanachain "github.com/macrowallets/waas/app/adapters/chain/solana"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// errActiveChainsNotLoaded means Boot has not read the chain catalog yet.
var errActiveChainsNotLoaded = errors.New("active chains were not loaded during boot")

var (
	activeChainMu     sync.Mutex
	activeChainsReady bool
	activeChainRows   []models.Chain
)

// openActiveChainEndpoint opens a sealed rpc_url. The container factory uses
// the process cipher. Tests replace this with a stub that never logs a URL.
var openActiveChainEndpoint = openChainEndpoint

type activeChainReader interface {
	FindActive(ctx context.Context) ([]models.Chain, error)
}

// loadActiveChains reads the active chain catalog in Boot.
// The container factory registers that catalog and does not query chains.
// A read failure is logged and leaves the catalog empty. The process stays up.
func loadActiveChains(app foundation.Application) {
	if app == nil {
		slog.Error("failed to load chains from DB", "error", errActiveChainsNotLoaded)
		rememberActiveChains(nil)
		return
	}
	repo, err := resolve[*repositories.ChainRepository](app)
	if err != nil || repo == nil {
		if err == nil {
			err = errActiveChainsNotLoaded
		}
		slog.Error("failed to load chains from DB", "error", err)
		rememberActiveChains(nil)
		return
	}
	readActiveChains(repo)
}

func readActiveChains(repo activeChainReader) {
	if repo == nil {
		slog.Error("failed to load chains from DB", "error", errActiveChainsNotLoaded)
		rememberActiveChains(nil)
		return
	}
	rows, err := repo.FindActive(context.Background())
	if err != nil {
		slog.Error("failed to load chains from DB", "error", err)
		rememberActiveChains(nil)
		return
	}
	rememberActiveChains(rows)
}

func rememberActiveChains(rows []models.Chain) {
	copied := make([]models.Chain, len(rows))
	copy(copied, rows)
	activeChainMu.Lock()
	activeChainRows = copied
	activeChainsReady = true
	activeChainMu.Unlock()
}

func bootedActiveChains() ([]models.Chain, bool) {
	activeChainMu.Lock()
	defer activeChainMu.Unlock()
	if !activeChainsReady {
		return nil, false
	}
	out := make([]models.Chain, len(activeChainRows))
	copy(out, activeChainRows)
	return out, true
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
			adapter = bitcoinchain.NewBitcoinLive(bitcoinchain.BitcoinConfig{
				ChainIDStr:    ch.ID,
				ChainName:     ch.Name,
				NativeSymbol:  ch.NativeSymbol,
				RPCURL:        rpcURL,
				Network:       network,
				IsTestnet:     ch.IsTestnet,
				Confirmations: uint64(ch.RequiredConfirmations),
			})
		case models.AdapterTypeSolana:
			adapter = solanachain.NewSolanaLive(solanachain.SolanaConfig{
				ChainIDStr:    ch.ID,
				ChainName:     ch.Name,
				NativeSymbol:  ch.NativeSymbol,
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
