package chaincatalog

import (
	"github.com/macrowallets/waas/app/models"
)

// XRP Ledger records, added after TRON and Litecoin. rpc_url is an env:NAME
// reference. The testnet network is the public altnet. There are no issued
// currencies in this experiment. Classic addresses are the same on mainnet and
// testnet; the record's network only chooses which server a read uses.

const (
	iconXRP           = cmcIconBase + "/52.png"
	xrpNativeDecimals = 6
)

// AddedXRPChainIDs are the XRP Ledger records chains:add-missing may create.
var AddedXRPChainIDs = []string{
	models.ChainXRP,
	models.ChainTXRP,
}

func (cat *Catalog) addedXRPChainSeeds() []chainSeed {
	return buildAddedXRPChainSeeds(cat.configuredConfirmations)
}

// buildAddedXRPChainSeeds lists the XRP Ledger records. confirmations returns
// the configured confirmations of the primary chain id, shared by txrp.
func buildAddedXRPChainSeeds(confirmations func(primaryChainID string) int) []chainSeed {
	xrpConfirmations := confirmations(models.ChainXRP)
	return []chainSeed{
		{id: models.ChainXRP, name: "XRP Ledger", adapterType: models.AdapterTypeXRP, nativeSymbol: models.NativeXRP, nativeDecimals: xrpNativeDecimals, envVar: "XRP_RPC_URL", requiredConfirmations: xrpConfirmations, displayOrder: 19, iconURL: iconXRP, rpcEnvReference: true},
		{id: models.ChainTXRP, name: "XRP Ledger Testnet", adapterType: models.AdapterTypeXRP, nativeSymbol: models.NativeXRP, nativeDecimals: xrpNativeDecimals, envVar: "TXRP_RPC_URL", isTestnet: true, mainnetChainID: strp(models.ChainXRP), requiredConfirmations: xrpConfirmations, displayOrder: 20, iconURL: iconXRP, rpcEnvReference: true},
	}
}

// xrpResources: the XRPL explorer uses /transactions and /accounts. The faucet
// is the public altnet faucet. Mainnet has no faucet.
var xrpResources = map[string][]resourceSeed{
	models.NetworkXRPLMainnet: {
		{resourceType: resourceTypeExplorer, name: "XRP Ledger Explorer", url: "https://livenet.xrpl.org"},
	},
	models.NetworkXRPLTestnet: {
		{resourceType: resourceTypeExplorer, name: "XRP Ledger Testnet Explorer", url: "https://testnet.xrpl.org"},
		{resourceType: resourceTypeFaucet, name: "XRP Ledger Testnet Faucet", url: "https://xrpl.org/resources/dev-tools/xrp-faucets"},
	},
}
