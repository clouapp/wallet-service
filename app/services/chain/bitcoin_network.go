package chain

import (
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/btcutil/bech32"
	"github.com/btcsuite/btcd/chaincfg"

	"github.com/macrowallets/waas/app/models"
)

// Fee asset symbols, uppercase like every FeeEstimate.FeeAsset; test networks carry
// a T so a testnet quote is never read as a mainnet one.
const (
	btcFeeAssetMainnet = "BTC"
	btcFeeAssetTestnet = "TBTC"
	ltcFeeAssetMainnet = "LTC"
	ltcFeeAssetTestnet = "TLTC"
)

const (
	p2wpkhWitnessVersion = 0
	p2wpkhProgramSize    = 20
	bech32DataGroupBits  = 5
	byteBits             = 8
)

// Genesis block hashes, the identity a fallback provider must prove before it is
// used (bitcoind getblockhash 0, Esplora /block-height/0, Electrum server.features).
const (
	btcMainnetGenesisHash  = "000000000019d6689c085ae165831e934ff763ae46a2a6c172b3f1b60a8ce26f"
	btcTestnet3GenesisHash = "000000000933ea01ad0ee984209779baaec3ced90fa3f408719526f8d77f4943"
	btcTestnet4GenesisHash = "00000000da84f2bafbbc53dee25a72ae507ff4914b867c565be350b0da8bf043"
	ltcMainnetGenesisHash  = "12a765e31ffd4059bada1e25190f6e98c99d9714d334efa41a195a7e7e04bfe2"
	ltcTestnetGenesisHash  = "4966625a4b2851d9fdee139e56211a0d88575f59ed816ff5e6a63deb4e3e29a0"
)

// esploraHosts are the providers spoken to over the Esplora REST API rather than
// bitcoind JSON-RPC.
var esploraHosts = []string{"blockstream.info", "mempool.space", "litecoinspace.org"}

// bitcoinNetwork is what differs between the Bitcoin-family networks the adapter
// runs on: address and key encoding, the fee asset, the dust limit and the order in
// which Esplora fee-rate sources are tried.
type bitcoinNetwork struct {
	name       string
	params     *chaincfg.Params
	testnet    bool
	feeAsset   string
	dustSats   int64
	feeSources []btcFeeSource
	// genesisHash is the network's block 0; empty skips the fallback network check.
	genesisHash string
}

// bitcoinNetworkOf picks the network from the chain record: Litecoin ids get the
// Litecoin parameters, the others Bitcoin's; test ids (tbtc, tltc) and records
// flagged is_testnet use the test network. Every Bitcoin test network (testnet3,
// testnet4, signet) shares one address and WIF encoding.
func bitcoinNetworkOf(cfg BitcoinConfig) bitcoinNetwork {
	testnet := cfg.IsTestnet || cfg.ChainIDStr == models.ChainTBTC || cfg.ChainIDStr == models.ChainTLTC
	if models.IsLitecoinChainID(cfg.ChainIDStr) {
		if testnet {
			return bitcoinNetwork{
				name: models.NetworkLitecoinTestnet, params: &ltcTestNetParams, testnet: true,
				feeAsset: ltcFeeAssetTestnet, dustSats: ltcDustSats, feeSources: litecoinFeeSources,
				genesisHash: ltcTestnetGenesisHash,
			}
		}
		return bitcoinNetwork{
			name: models.NetworkLitecoinMainnet, params: &ltcMainNetParams,
			feeAsset: ltcFeeAssetMainnet, dustSats: ltcDustSats, feeSources: litecoinFeeSources,
			genesisHash: ltcMainnetGenesisHash,
		}
	}
	if testnet {
		name, genesis := models.NetworkBitcoinTestnet, btcTestnet3GenesisHash
		if models.IsBitcoinTestnet4RPCURL(cfg.RPCURL) {
			name, genesis = models.NetworkBitcoinTestnet4, btcTestnet4GenesisHash
		}
		return bitcoinNetwork{
			name: name, params: &chaincfg.TestNet3Params, testnet: true,
			feeAsset: btcFeeAssetTestnet, dustSats: btcDustSats, feeSources: bitcoinFeeSources,
			genesisHash: genesis,
		}
	}
	return bitcoinNetwork{
		name: models.NetworkBitcoinMainnet, params: &chaincfg.MainNetParams,
		feeAsset: btcFeeAssetMainnet, dustSats: btcDustSats, feeSources: bitcoinFeeSources,
		genesisHash: btcMainnetGenesisHash,
	}
}

// BitcoinFamilyParams are the address and key parameters of the network a
// Bitcoin-family chain record (btc, tbtc, ltc, tltc) transacts on. The result is a
// copy; nil for any other chain.
func BitcoinFamilyParams(chainID string, testnet bool) *chaincfg.Params {
	if !models.IsBitcoinFamilyChainID(chainID) {
		return nil
	}
	params := *bitcoinNetworkOf(BitcoinConfig{ChainIDStr: chainID, IsTestnet: testnet}).params
	return &params
}

func isEsploraURL(rpcURL string) bool {
	for _, host := range esploraHosts {
		if strings.Contains(rpcURL, host) {
			return true
		}
	}
	return false
}

// witnessProgram is the 20-byte program of a native SegWit v0 P2WPKH address of net
// (bc1q/tb1q, ltc1q/tltc1q; all-uppercase accepted as BIP-173 allows). The builder
// only pays and spends P2WPKH, so P2WSH, taproot, legacy base58 and addresses of
// another network are refused. Decoding is done here rather than by
// btcutil.DecodeAddress, which only recognizes bech32 prefixes of networks registered
// with chaincfg and does not check that the prefix is net's.
func witnessProgram(address string, net *chaincfg.Params) ([]byte, error) {
	if net == nil {
		return nil, fmt.Errorf("btc address %s: network parameters are required", address)
	}
	hrp, data, encoding, err := bech32.DecodeGeneric(address)
	if err != nil {
		return nil, fmt.Errorf("btc address %s is not a bech32 SegWit address: %w", address, err)
	}
	if hrp != net.Bech32HRPSegwit {
		return nil, fmt.Errorf("btc address %s is for prefix %q, not %s (%q)", address, hrp, net.Name, net.Bech32HRPSegwit)
	}
	if len(data) == 0 || data[0] != p2wpkhWitnessVersion || encoding != bech32.Version0 {
		return nil, fmt.Errorf("btc address %s is not p2wpkh: only SegWit v0 bech32 addresses are paid", address)
	}
	program, err := bech32.ConvertBits(data[1:], bech32DataGroupBits, byteBits, false)
	if err != nil {
		return nil, fmt.Errorf("btc address %s: %w", address, err)
	}
	if len(program) != p2wpkhProgramSize {
		return nil, fmt.Errorf("btc address %s is not p2wpkh: %d-byte witness program", address, len(program))
	}
	return program, nil
}
