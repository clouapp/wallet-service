package bitcoin

import (
	"slices"

	"github.com/macrowallets/waas/app/models"
)

// Public Litecoin providers used as fallbacks when <PREFIX>_FALLBACK_RPC_URL is
// empty. Each one proves its network by the genesis block before it is used.
//
// Self-signed ElectrumX servers are pinned to the SHA-256 of their leaf certificate
// (DER). When an operator rotates it, the server is refused ("does not match the
// pin") and the next provider serves; re-read the pin with
//
//	openssl s_client -connect HOST:PORT -servername HOST </dev/null | openssl x509 -outform DER | sha256sum
//
// and check server.features' genesis_hash before updating it here. Servers with a
// CA-issued certificate (cipig.net, Let's Encrypt, renewed every ~90 days) carry no
// pin: the system roots and the host name verify them.
const (
	// Litecoin mainnet ElectrumX (checked 2026-10-05: ElectrumX 1.15-2.0, protocol
	// 1.4, mainnet genesis). The bysh.me and xurious certificates are the ones of
	// their testnet ports and run until 2036; backup.electrum-ltc.org's until 2037.
	ltcMainnetElectrumBysh    = "electrum+ssl://electrum-ltc.bysh.me:50002?cert_sha256=fdf3c121181c14100d8740007ed546de388099c7090315ca2cf995bb4620c41d"
	ltcMainnetElectrumBackup  = "electrum+ssl://backup.electrum-ltc.org:443?cert_sha256=6a7119f31b4784e9c56cfb7c054975e2337b009fb60bf15e409d3bcc159e39e3"
	ltcMainnetElectrumCipig   = "electrum+ssl://electrum1.cipig.net:20063"
	ltcMainnetElectrumXurious = "electrum+ssl://electrum.ltc.xurious.com:50002?cert_sha256=e3aedd3093856098e2efe66d96fb7cd97adae8eeda1d070d460e84dc5134cf26"
	// ltcMainnetTatumGateway is Tatum's Litecoin mainnet RPC gateway (keyless:
	// 5 requests/min; no UTXO lookup without a key).
	ltcMainnetTatumGateway = "https://litecoin-mainnet.gateway.tatum.io"

	ltcTestnetElectrumBysh    = "electrum+ssl://electrum-ltc.bysh.me:51002?cert_sha256=fdf3c121181c14100d8740007ed546de388099c7090315ca2cf995bb4620c41d"
	ltcTestnetElectrumXurious = "electrum+ssl://electrum.ltc.xurious.com:51002?cert_sha256=e3aedd3093856098e2efe66d96fb7cd97adae8eeda1d070d460e84dc5134cf26"
	// ltcTestnetTatumGateway is Tatum's Litecoin testnet RPC gateway.
	ltcTestnetTatumGateway = "https://litecoin-testnet.gateway.tatum.io"
)

// defaultBitcoinFallbacks are the built-in fallbacks by network, in the order tried:
// ElectrumX servers of different operators first (UTXOs, balance, tip, fee, status,
// broadcast; no block listing), Tatum last (block scans, and UTXOs with a key).
// Bitcoin networks have none: their fallbacks come from configuration only.
var defaultBitcoinFallbacks = map[string][]string{
	models.NetworkLitecoinMainnet: {
		ltcMainnetElectrumBysh, ltcMainnetElectrumBackup, ltcMainnetElectrumCipig, ltcMainnetElectrumXurious,
		ltcMainnetTatumGateway,
	},
	models.NetworkLitecoinTestnet: {ltcTestnetElectrumBysh, ltcTestnetElectrumXurious, ltcTestnetTatumGateway},
}

// DefaultBitcoinFallbackURLs are the built-in fallbacks of the network cfg resolves
// to (bitcoinNetworkOf), or none.
func DefaultBitcoinFallbackURLs(cfg BitcoinConfig) []string {
	return slices.Clone(defaultBitcoinFallbacks[bitcoinNetworkOf(cfg).name])
}
