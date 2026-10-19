package rpcendpoints

import "github.com/macrowallets/waas/app/models"

// defaultFallbacks are the fallback endpoints chains:rpc-endpoints --defaults
// proposes per network, in order. Only public, keyless endpoints checked by hand
// belong here; adapters never read this list.
//
// Sepolia, checked on 2026-10-18/19: sepolia.drpc.org and sepolia.gateway.tenderly.co
// answered eth_getTransactionReceipt for a mined transaction 3 of 3 times and serve
// eth_getLogs (50-block window) and eth_getBlockReceipts; the load-balanced
// ethereum-sepolia-rpc.publicnode.com answered null 6 of 6 times for that receipt,
// so it comes last. 1rpc.io/sepolia rate-limits eth_getLogs and refuses
// eth_getBlockReceipts and is not listed.
var defaultFallbacks = map[string][]string{
	models.NetworkEthereumSepolia: {
		"https://sepolia.drpc.org",
		"https://sepolia.gateway.tenderly.co",
		"https://ethereum-sepolia-rpc.publicnode.com",
	},
}

// DefaultFallbacks are the proposed fallback endpoints of network, in order, or
// none for a network without a checked list.
func DefaultFallbacks(network string) []string {
	return append([]string(nil), defaultFallbacks[network]...)
}
