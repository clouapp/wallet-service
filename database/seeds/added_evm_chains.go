package seeds

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/config"
	"github.com/macrowallets/waas/pkg/numeric"
)

// The EVM records added after eth/btc/polygon/sol. Unlike those, everything that
// depends on the network (tokens, explorers, faucets) is chosen by the network the
// record points at, so a profile-flipped "base" on Base Sepolia gets the Base
// Sepolia USDC contract, and rpc_url is stored as an env:NAME reference.

const (
	confirmationsConfigKey = "vault.chains.required_confirmations."
	resourceTypeExplorer   = "explorer"
	resourceTypeFaucet     = "faucet"
	llamaoChainIconBase    = "https://icons.llamao.fi/icons/chains"
)

const (
	iconBase     = llamaoChainIconBase + "/rsz_base.jpg"
	iconArbitrum = cmcIconBase + "/11841.png"
	iconBNB      = cmcIconBase + "/1839.png"
	iconUSDC     = cmcIconBase + "/3408.png"
	iconUSDT     = cmcIconBase + "/825.png"
)

// AddedEVMChainIDs are the chain records chains:add-missing may create.
var AddedEVMChainIDs = []string{
	models.ChainBase, models.ChainArbitrum, models.ChainBSC,
	models.ChainTBase, models.ChainTArbitrum, models.ChainTBSC,
}

func addedEVMChainSeeds() []chainSeed {
	return buildAddedEVMChainSeeds(configuredConfirmations)
}

// buildAddedEVMChainSeeds lists the added records; confirmations returns the
// configured confirmations of a primary chain id (shared by its test record).
func buildAddedEVMChainSeeds(confirmations func(primaryChainID string) int) []chainSeed {
	baseConfirmations := confirmations(models.ChainBase)
	arbitrumConfirmations := confirmations(models.ChainArbitrum)
	bscConfirmations := confirmations(models.ChainBSC)
	return []chainSeed{
		{id: models.ChainBase, name: "Base", adapterType: models.AdapterTypeEVM, nativeSymbol: models.NativeETH, nativeDecimals: 18, networkID: i64p(models.EVMNetworkIDBaseMainnet), envVar: "BASE_RPC_URL", requiredConfirmations: baseConfirmations, displayOrder: 9, iconURL: iconBase, rpcEnvReference: true},
		{id: models.ChainArbitrum, name: "Arbitrum One", adapterType: models.AdapterTypeEVM, nativeSymbol: models.NativeETH, nativeDecimals: 18, networkID: i64p(models.EVMNetworkIDArbitrumMainnet), envVar: "ARBITRUM_RPC_URL", requiredConfirmations: arbitrumConfirmations, displayOrder: 11, iconURL: iconArbitrum, rpcEnvReference: true},
		{id: models.ChainBSC, name: "BNB Smart Chain", adapterType: models.AdapterTypeEVM, nativeSymbol: models.NativeBNB, nativeDecimals: 18, networkID: i64p(models.EVMNetworkIDBSCMainnet), envVar: "BSC_RPC_URL", requiredConfirmations: bscConfirmations, displayOrder: 13, iconURL: iconBNB, rpcEnvReference: true},
		{id: models.ChainTBase, name: "Base Sepolia", adapterType: models.AdapterTypeEVM, nativeSymbol: models.NativeETH, nativeDecimals: 18, networkID: i64p(models.EVMNetworkIDBaseSepolia), envVar: "TBASE_RPC_URL", isTestnet: true, mainnetChainID: strp(models.ChainBase), requiredConfirmations: baseConfirmations, displayOrder: 10, iconURL: iconBase, rpcEnvReference: true},
		{id: models.ChainTArbitrum, name: "Arbitrum Sepolia", adapterType: models.AdapterTypeEVM, nativeSymbol: models.NativeETH, nativeDecimals: 18, networkID: i64p(models.EVMNetworkIDArbitrumSepolia), envVar: "TARBITRUM_RPC_URL", isTestnet: true, mainnetChainID: strp(models.ChainArbitrum), requiredConfirmations: arbitrumConfirmations, displayOrder: 12, iconURL: iconArbitrum, rpcEnvReference: true},
		{id: models.ChainTBSC, name: "BNB Smart Chain Testnet", adapterType: models.AdapterTypeEVM, nativeSymbol: models.NativeBNB, nativeDecimals: 18, networkID: i64p(models.EVMNetworkIDBSCTestnet), envVar: "TBSC_RPC_URL", isTestnet: true, mainnetChainID: strp(models.ChainBSC), requiredConfirmations: bscConfirmations, displayOrder: 14, iconURL: iconBNB, rpcEnvReference: true},
	}
}

func configuredConfirmations(primaryChainID string) int {
	return facades.Config().GetInt(confirmationsConfigKey + primaryChainID)
}

type tokenSeed struct {
	chainID         string
	symbol          string
	name            string
	contractAddress string
	decimals        int
	iconURL         string
}

// networkTokens are the tokens of each added network. Base and Arbitrum Sepolia
// only have Circle's USDC; BSC testnet has no issuer-deployed stablecoin, so it is
// native-only. Decimals were read on-chain: BSC's Binance-Peg USDT/USDC use 18.
var networkTokens = map[string][]tokenSeed{
	models.NetworkBaseMainnet: {
		{symbol: models.SymbolUSDC, name: "USD Coin", contractAddress: models.USDCContractBase, decimals: 6, iconURL: iconUSDC},
	},
	models.NetworkBaseSepolia: {
		{symbol: models.SymbolUSDC, name: "USD Coin (Test)", contractAddress: models.USDCContractBaseSepolia, decimals: 6, iconURL: iconUSDC},
	},
	models.NetworkArbitrumMainnet: {
		{symbol: models.SymbolUSDC, name: "USD Coin", contractAddress: models.USDCContractArbitrum, decimals: 6, iconURL: iconUSDC},
		{symbol: models.SymbolUSDT, name: "USD₮0", contractAddress: models.USDTContractArbitrum, decimals: 6, iconURL: iconUSDT},
	},
	models.NetworkArbitrumSepolia: {
		{symbol: models.SymbolUSDC, name: "USD Coin (Test)", contractAddress: models.USDCContractArbSepolia, decimals: 6, iconURL: iconUSDC},
	},
	models.NetworkBSCMainnet: {
		{symbol: models.SymbolUSDT, name: "Binance-Peg Tether USD", contractAddress: models.USDTContractBSC, decimals: 18, iconURL: iconUSDT},
		{symbol: models.SymbolUSDC, name: "Binance-Peg USD Coin", contractAddress: models.USDCContractBSC, decimals: 18, iconURL: iconUSDC},
	},
}

type resourceSeed struct {
	chainID      string
	resourceType string
	name         string
	url          string
}

// networkResources are explorers whose transaction pages open without a login or
// bot challenge (the Basescan, Arbiscan and BscScan pages answer 403 with a
// Cloudflare check) and the networks' faucets.
var networkResources = map[string][]resourceSeed{
	models.NetworkBaseMainnet:     {{resourceType: resourceTypeExplorer, name: "Base Blockscout", url: "https://base.blockscout.com"}},
	models.NetworkBaseSepolia:     {{resourceType: resourceTypeExplorer, name: "Base Sepolia Blockscout", url: "https://base-sepolia.blockscout.com"}, {resourceType: resourceTypeFaucet, name: "Base Sepolia Faucet", url: "https://portal.cdp.coinbase.com/products/faucet"}},
	models.NetworkArbitrumMainnet: {{resourceType: resourceTypeExplorer, name: "Arbitrum One Blockscout", url: "https://arbitrum.blockscout.com"}},
	models.NetworkArbitrumSepolia: {{resourceType: resourceTypeExplorer, name: "Arbitrum Sepolia Blockscout", url: "https://arbitrum-sepolia.blockscout.com"}, {resourceType: resourceTypeFaucet, name: "Arbitrum Sepolia Faucet", url: "https://www.alchemy.com/faucets/arbitrum-sepolia"}},
	models.NetworkBSCMainnet:      {{resourceType: resourceTypeExplorer, name: "BscTrace", url: "https://bsctrace.com"}},
	models.NetworkBSCTestnet:      {{resourceType: resourceTypeExplorer, name: "BscTrace Testnet", url: "https://testnet.bsctrace.com"}, {resourceType: resourceTypeFaucet, name: "BNB Smart Chain Testnet Faucet", url: "https://www.bnbchain.org/en/testnet-faucet"}},
}

// addedChainNetwork is the network an added record points at under profile.
func addedChainNetwork(c chainSeed, profile string) (string, error) {
	c, err := withProfileNetwork(c, profile)
	if err != nil {
		return "", err
	}
	if c.networkID == nil {
		return "", fmt.Errorf("chain %s has no network id", c.id)
	}
	network := models.EVMNetworkName(*c.networkID)
	if network == "" {
		return "", fmt.Errorf("chain %s: unknown EVM network id %d", c.id, *c.networkID)
	}
	return network, nil
}

func addedChainTokens(profile string) ([]tokenSeed, error) {
	return tokensForChains(addedEVMChainSeeds(), profile)
}

func tokensForChains(chains []chainSeed, profile string) ([]tokenSeed, error) {
	tokens := make([]tokenSeed, 0)
	for _, c := range chains {
		network, err := addedChainNetwork(c, profile)
		if err != nil {
			return nil, err
		}
		for _, t := range networkTokens[network] {
			t.chainID = c.id
			tokens = append(tokens, t)
		}
	}
	return tokens, nil
}

func addedChainResources(profile string) ([]resourceSeed, error) {
	return resourcesForChains(addedEVMChainSeeds(), profile)
}

func resourcesForChains(chains []chainSeed, profile string) ([]resourceSeed, error) {
	resources := make([]resourceSeed, 0)
	for _, c := range chains {
		network, err := addedChainNetwork(c, profile)
		if err != nil {
			return nil, err
		}
		for _, r := range networkResources[network] {
			r.chainID = c.id
			resources = append(resources, r)
		}
	}
	return resources, nil
}

type seedThresholds struct {
	gasReadinessRaw *string
	dustNativeRaw   *string
	dustUSD         numeric.NullDecimal
}

// addedChainThresholds are the sweep thresholds of an added record, from the
// env-overridable config.SweepDefaults.
func addedChainThresholds(chainID string) (seedThresholds, error) {
	defaults, ok := config.SweepDefaults()[chainID]
	if !ok {
		return seedThresholds{}, fmt.Errorf("no sweep defaults for chain %s", chainID)
	}
	if err := models.DustThresholdUSDColumn.Validate(defaults.DustUSD); err != nil {
		return seedThresholds{}, fmt.Errorf("sweep defaults for chain %s: %w", chainID, err)
	}
	return seedThresholds{
		gasReadinessRaw: nonEmptyPtr(defaults.GasReadinessRaw),
		dustNativeRaw:   nonEmptyPtr(defaults.DustNativeRaw),
		dustUSD:         numeric.NewNullDecimal(defaults.DustUSD),
	}, nil
}

func nonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// AddedChainsResult is what SeedMissingAddedChains created, or would create.
type AddedChainsResult struct {
	Chains    []AddedChain
	Tokens    []string
	Resources []string
	Skipped   []string
}

// AddedChain is a record SeedMissingAddedChains creates: its rpc_url is
// "env:<EnvVar>" and it signs for Network.
type AddedChain struct {
	ID        string
	EnvVar    string
	Network   string
	NetworkID int64
	IsTestnet bool
}

// SeedMissingAddedChains creates the added EVM records that are not in the registry
// yet, with their thresholds, tokens and resources. It never updates an existing row
// (unlike SeedChains, which re-encrypts every rpc_url), so it is safe on a live
// database. With apply false it only reports what it would create.
func SeedMissingAddedChains(_ context.Context, apply bool) (AddedChainsResult, error) {
	var result AddedChainsResult
	profile, err := configuredChainNetworkProfile()
	if err != nil {
		return result, err
	}
	if profile == "" {
		return result, fmt.Errorf("CHAIN_NETWORK_PROFILE is required: it decides which network base/arbitrum/bsc point at")
	}
	tokens, err := addedChainTokens(profile)
	if err != nil {
		return result, err
	}
	resources, err := addedChainResources(profile)
	if err != nil {
		return result, err
	}

	ordered := make([]chainSeed, 0)
	for _, c := range chainSeeds() {
		if isAddedEVMChain(c.id) {
			ordered = append(ordered, c)
		}
	}
	for _, c := range ordered {
		exists, err := chainExists(c.id)
		if err != nil {
			return result, err
		}
		if exists {
			result.Skipped = append(result.Skipped, c.id)
			continue
		}
		network, err := addedChainNetwork(c, profile)
		if err != nil {
			return result, err
		}
		if c, err = withProfileNetwork(c, profile); err != nil {
			return result, err
		}
		result.Chains = append(result.Chains, AddedChain{
			ID: c.id, EnvVar: c.envVar, Network: network, NetworkID: *c.networkID, IsTestnet: c.isTestnet,
		})
		if !apply {
			continue
		}
		thresholds, err := addedChainThresholds(c.id)
		if err != nil {
			return result, err
		}
		encRPC, err := encryptSeedRPC(c)
		if err != nil {
			return result, fmt.Errorf("encrypt RPC for chain %s: %w", c.id, err)
		}
		if err := createSeedChain(c, encRPC, &thresholds); err != nil {
			return result, err
		}
	}

	created := make(map[string]bool, len(result.Chains))
	for _, c := range result.Chains {
		created[c.ID] = true
	}
	for _, t := range tokens {
		if !created[t.chainID] {
			continue
		}
		result.Tokens = append(result.Tokens, t.chainID+":"+t.symbol+":"+t.contractAddress)
		if apply {
			if _, err := createTokenUnlessPresent(t); err != nil {
				return result, err
			}
		}
	}
	for _, r := range resources {
		if !created[r.chainID] {
			continue
		}
		result.Resources = append(result.Resources, r.chainID+":"+r.resourceType+":"+r.url)
		if apply {
			if _, err := createResourceUnlessPresent(r); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

func isAddedEVMChain(chainID string) bool {
	for _, id := range AddedEVMChainIDs {
		if id == chainID {
			return true
		}
	}
	return false
}

func chainExists(chainID string) (bool, error) {
	count, err := facades.Orm().Query().Model(&models.Chain{}).Where("id = ?", chainID).Count()
	if err != nil {
		return false, fmt.Errorf("look up chain %s: %w", chainID, err)
	}
	return count > 0, nil
}

// createTokenUnlessPresent inserts t unless the chain already lists its contract.
func createTokenUnlessPresent(t tokenSeed) (bool, error) {
	var existing models.Token
	q := facades.Orm().Query().
		Where("chain_id", t.chainID).
		Where("contract_address", t.contractAddress)
	if err := q.First(&existing); err == nil && existing.ID != uuid.Nil {
		return false, nil
	}
	iconURL := t.iconURL
	tok := models.Token{
		ID:              uuid.New(),
		ChainID:         t.chainID,
		Symbol:          t.symbol,
		Name:            t.name,
		ContractAddress: t.contractAddress,
		Decimals:        t.decimals,
		IconURL:         &iconURL,
		Status:          "active",
	}
	if err := facades.Orm().Query().Create(&tok); err != nil {
		return false, fmt.Errorf("create token %s %s: %w", t.chainID, t.symbol, err)
	}
	slog.Info("created token", "chain_id", t.chainID, "symbol", t.symbol)
	return true, nil
}

// createResourceUnlessPresent inserts r unless the chain has a resource of that
// type and name.
func createResourceUnlessPresent(r resourceSeed) (bool, error) {
	var existing models.ChainResource
	err := facades.Orm().Query().
		Where("chain_id", r.chainID).
		Where("type", r.resourceType).
		Where("name", r.name).
		First(&existing)
	if err == nil && existing.ID != uuid.Nil {
		return false, nil
	}
	cr := models.ChainResource{
		ID:      uuid.New(),
		ChainID: r.chainID,
		Type:    r.resourceType,
		Name:    r.name,
		URL:     r.url,
		Status:  "active",
	}
	if err := facades.Orm().Query().Create(&cr); err != nil {
		return false, fmt.Errorf("create chain resource %s %s: %w", r.chainID, r.name, err)
	}
	slog.Info("created chain resource", "chain_id", r.chainID, "name", r.name)
	return true, nil
}
