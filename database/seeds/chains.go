package seeds

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/pkg/types"
)

const cmcIconBase = "https://s2.coinmarketcap.com/static/img/coins/64x64"

type chainSeed struct {
	id                    string
	name                  string
	adapterType           string
	nativeSymbol          string
	nativeDecimals        int
	networkID             *int64
	envVar                string
	isTestnet             bool
	mainnetChainID        *string
	requiredConfirmations int
	displayOrder          int
	iconURL               string
	// rpcEnvReference stores rpc_url as "env:<envVar>" instead of the URL itself,
	// so the URL (and any API key in it) is resolved from the environment at boot.
	rpcEnvReference bool
}

// chainSeeds lists every chain record, mainnets before testnets (FK targets first).
func chainSeeds() []chainSeed {
	return chainSeedsWith(addedChainSeeds())
}

func chainSeedsWith(added []chainSeed) []chainSeed {
	mainnets := []chainSeed{
		{id: models.ChainETH, name: "Ethereum", adapterType: models.AdapterTypeEVM, nativeSymbol: "eth", nativeDecimals: 18, networkID: i64p(1), envVar: "ETH_RPC_URL", requiredConfirmations: 12, displayOrder: 1, iconURL: cmcIconBase + "/1027.png"},
		{id: models.ChainBTC, name: "Bitcoin", adapterType: models.AdapterTypeBitcoin, nativeSymbol: "btc", nativeDecimals: 8, envVar: "BTC_RPC_URL", requiredConfirmations: 6, displayOrder: 3, iconURL: cmcIconBase + "/1.png"},
		{id: models.ChainPolygon, name: "Polygon", adapterType: models.AdapterTypeEVM, nativeSymbol: types.NativeSymbolPOL, nativeDecimals: 18, networkID: i64p(137), envVar: "POLYGON_RPC_URL", requiredConfirmations: 128, displayOrder: 5, iconURL: cmcIconBase + "/3890.png"},
		{id: models.ChainSOL, name: "Solana", adapterType: models.AdapterTypeSolana, nativeSymbol: "sol", nativeDecimals: 9, envVar: "SOLANA_RPC_URL", requiredConfirmations: 1, displayOrder: 7, iconURL: cmcIconBase + "/5426.png"},
	}
	testnets := []chainSeed{
		{id: models.ChainTETH, name: "Sepolia", adapterType: models.AdapterTypeEVM, nativeSymbol: "eth", nativeDecimals: 18, networkID: i64p(11155111), envVar: "TETH_RPC_URL", isTestnet: true, mainnetChainID: strp(models.ChainETH), requiredConfirmations: 12, displayOrder: 2, iconURL: cmcIconBase + "/1027.png"},
		{id: models.ChainTBTC, name: "Bitcoin Testnet", adapterType: models.AdapterTypeBitcoin, nativeSymbol: "btc", nativeDecimals: 8, envVar: "TBTC_RPC_URL", isTestnet: true, mainnetChainID: strp(models.ChainBTC), requiredConfirmations: 6, displayOrder: 4, iconURL: cmcIconBase + "/1.png"},
		{id: models.ChainTPolygon, name: "Polygon Amoy", adapterType: models.AdapterTypeEVM, nativeSymbol: types.NativeSymbolPOL, nativeDecimals: 18, networkID: i64p(80002), envVar: "TPOLYGON_RPC_URL", isTestnet: true, mainnetChainID: strp(models.ChainPolygon), requiredConfirmations: 128, displayOrder: 6, iconURL: cmcIconBase + "/3890.png"},
		{id: models.ChainTSOL, name: "Solana Devnet", adapterType: models.AdapterTypeSolana, nativeSymbol: "sol", nativeDecimals: 9, envVar: "TSOL_RPC_URL", isTestnet: true, mainnetChainID: strp(models.ChainSOL), requiredConfirmations: 1, displayOrder: 8, iconURL: cmcIconBase + "/5426.png"},
	}
	all := make([]chainSeed, 0, len(mainnets)+len(testnets)+len(added))
	all = append(all, mainnets...)
	for _, c := range added {
		if !c.isTestnet {
			all = append(all, c)
		}
	}
	all = append(all, testnets...)
	for _, c := range added {
		if c.isTestnet {
			all = append(all, c)
		}
	}
	return all
}

// SeedChains inserts chain rows (mainnets before testnets for FK targets). With
// CHAIN_NETWORK_PROFILE set, the primary records (eth, btc, polygon, sol, base,
// arbitrum, bsc) are created on that profile's networks and existing rows are
// realigned to it (chains:align-network).
func SeedChains(ctx context.Context) error {
	profile, err := configuredChainNetworkProfile()
	if err != nil {
		return err
	}

	for _, c := range chainSeeds() {
		c, err = withProfileNetwork(c, profile)
		if err != nil {
			return err
		}

		encRPC, err := encryptSeedRPC(c)
		if err != nil {
			return fmt.Errorf("encrypt RPC for chain %s: %w", c.id, err)
		}

		var existing models.Chain
		if err := facades.Orm().Query().Where("id", c.id).First(&existing); err == nil && existing.ID != "" {
			// Re-encrypt rpc_url under the current APP_KEY so a rotated key self-heals
			// on re-seed instead of leaving the registry unable to decrypt (which shows
			// up as "unknown chain" for every API call).
			if _, err := facades.Orm().Query().Model(&models.Chain{}).
				Where("id = ?", c.id).
				Update("rpc_url", encRPC); err != nil {
				return fmt.Errorf("refresh rpc_url for chain %s: %w", c.id, err)
			}
			slog.Info("chain exists, refreshed rpc_url", "id", c.id)
			continue
		}

		if err := createSeedChain(c, encRPC, nil); err != nil {
			return err
		}
	}
	if profile == "" {
		return nil
	}
	return alignSeededChains(ctx, profile)
}

// withProfileNetwork points a primary record at the network profile selects; the
// t-prefixed records and an empty profile keep the seed's own network.
func withProfileNetwork(c chainSeed, profile string) (chainSeed, error) {
	if profile == "" {
		return c, nil
	}
	spec, decided, err := models.PrimaryChainNetwork(profile, c.id)
	if err != nil {
		return chainSeed{}, err
	}
	if decided {
		c.networkID = spec.NetworkID
		c.isTestnet = spec.IsTestnet
	}
	return c, nil
}

func encryptSeedRPC(c chainSeed) (string, error) {
	if c.rpcEnvReference {
		return facades.Crypt().EncryptString(models.RPCURLEnvPrefix + c.envVar)
	}
	return encryptRPCFromEnv(c.envVar)
}

func createSeedChain(c chainSeed, encRPC string, thresholds *seedThresholds) error {
	if c.requiredConfirmations <= 0 {
		return fmt.Errorf("create chain %s: required confirmations must be positive, got %d", c.id, c.requiredConfirmations)
	}
	iconURL := c.iconURL
	ch := models.Chain{
		ID:                    c.id,
		Name:                  c.name,
		AdapterType:           c.adapterType,
		NativeSymbol:          c.nativeSymbol,
		NativeDecimals:        c.nativeDecimals,
		NetworkID:             c.networkID,
		RpcURL:                encRPC,
		IsTestnet:             c.isTestnet,
		MainnetChainID:        c.mainnetChainID,
		RequiredConfirmations: c.requiredConfirmations,
		IconURL:               &iconURL,
		DisplayOrder:          c.displayOrder,
		Status:                "active",
	}
	if thresholds != nil {
		ch.GasReadinessThresholdRaw = thresholds.gasReadinessRaw
		ch.DustThresholdNativeRaw = thresholds.dustNativeRaw
		ch.DustThresholdUSD = thresholds.dustUSD
	}
	if err := facades.Orm().Query().Create(&ch); err != nil {
		return fmt.Errorf("create chain %s: %w", c.id, err)
	}
	slog.Info("created chain", "id", c.id)
	return nil
}

// alignSeededChains realigns rows that existed before the seed. No RPC probe: the
// seed runs offline; chains:align-network checks the RPCs.
func alignSeededChains(ctx context.Context, profile string) error {
	store := chainregistry.NewORMStore()
	alignment, err := chainregistry.PlanAlignment(ctx, profile, store, facades.Crypt().DecryptString, nil, uuid.Nil)
	if err != nil {
		return fmt.Errorf("align chains to the %s profile: %w", profile, err)
	}
	if err := chainregistry.ApplyAlignment(store, alignment); err != nil {
		return fmt.Errorf("align chains to the %s profile: %w", profile, err)
	}
	for _, change := range alignment.Plan.Changes {
		slog.Info("aligned chain", "id", change.ChainID, "from", change.FromNetwork, "to", change.ToNetwork, "is_testnet", change.ToTestnet)
	}
	for chainID, reissues := range alignment.Reissues {
		slog.Info("reissued addresses", "chain", chainID, "wallets", len(reissues))
	}
	return nil
}
