package addressing

import (
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"

	"github.com/macrowallets/waas/app/models"
)

// Litecoin address and key prefixes, magic and ports, from litecoin-project/litecoin
// src/chainparams.cpp (CMainParams, CTestNetParams) at commit
// ec1b6489a900d09cf5991e220dce089c77a232a2. The P2SH prefix is SCRIPT_ADDRESS2 (M... on
// mainnet, Q... on testnet), what Litecoin Core encodes; the legacy SCRIPT_ADDRESS
// (0x05 / 0xc4, the Bitcoin P2SH bytes) still decodes in Litecoin Core but is never
// paid here: the builder only pays native SegWit P2WPKH (ltc1q / tltc1q).
const (
	ltcMainNetMagic          wire.BitcoinNet = 0xdbb6c0fb // pchMessageStart fb c0 b6 db
	ltcMainNetPort                           = "9333"
	ltcMainNetPubKeyHashID   byte            = 0x30 // 48, L...
	ltcMainNetScriptHashID   byte            = 0x32 // 50, M... (legacy 0x05, 3...)
	ltcMainNetPrivateKeyID   byte            = 0xb0 // 176, WIF T... (compressed)
	ltcMainNetSLIP44CoinType uint32          = 2
	ltcTestNetMagic          wire.BitcoinNet = 0xf1c8d2fd // pchMessageStart fd d2 c8 f1
	ltcTestNetPort                           = "19335"
	ltcTestNetPubKeyHashID   byte            = 0x6f // 111, m.../n...
	ltcTestNetScriptHashID   byte            = 0x3a // 58, Q... (legacy 0xc4, 2...)
	ltcTestNetPrivateKeyID   byte            = 0xef // 239, WIF c... (compressed), as Bitcoin testnet
	ltcTestNetSLIP44CoinType uint32          = 1
	ltcMainNetParamsName                     = "litecoin-mainnet"
	ltcTestNetParamsName                     = "litecoin-testnet"
)

var (
	ltcMainNetHDPrivateKeyID = [4]byte{0x04, 0x88, 0xad, 0xe4} // xprv, as Bitcoin mainnet
	ltcMainNetHDPublicKeyID  = [4]byte{0x04, 0x88, 0xb2, 0x1e} // xpub
	ltcTestNetHDPrivateKeyID = [4]byte{0x04, 0x35, 0x83, 0x94} // tprv
	ltcTestNetHDPublicKeyID  = [4]byte{0x04, 0x35, 0x87, 0xcf} // tpub
)

// The Litecoin parameters are never registered with chaincfg.Register: registration
// is process-global and would make btcutil decode ltc1 strings for every caller.
var (
	litecoinMainNetParams = chaincfg.Params{
		Name:             ltcMainNetParamsName,
		Net:              ltcMainNetMagic,
		DefaultPort:      ltcMainNetPort,
		Bech32HRPSegwit:  LtcHRPMainnet,
		PubKeyHashAddrID: ltcMainNetPubKeyHashID,
		ScriptHashAddrID: ltcMainNetScriptHashID,
		PrivateKeyID:     ltcMainNetPrivateKeyID,
		HDPrivateKeyID:   ltcMainNetHDPrivateKeyID,
		HDPublicKeyID:    ltcMainNetHDPublicKeyID,
		HDCoinType:       ltcMainNetSLIP44CoinType,
	}
	litecoinTestNetParams = chaincfg.Params{
		Name:             ltcTestNetParamsName,
		Net:              ltcTestNetMagic,
		DefaultPort:      ltcTestNetPort,
		Bech32HRPSegwit:  LtcHRPTestnet,
		PubKeyHashAddrID: ltcTestNetPubKeyHashID,
		ScriptHashAddrID: ltcTestNetScriptHashID,
		PrivateKeyID:     ltcTestNetPrivateKeyID,
		HDPrivateKeyID:   ltcTestNetHDPrivateKeyID,
		HDPublicKeyID:    ltcTestNetHDPublicKeyID,
		HDCoinType:       ltcTestNetSLIP44CoinType,
	}
)

// BitcoinFamilyParams are the address and key parameters of the network a
// Bitcoin-family chain record (btc, tbtc, ltc, tltc) transacts on: Bitcoin's
// come from btcd, Litecoin's are defined here. Every Bitcoin test network
// (testnet3, testnet4, signet) shares one address and WIF encoding. The result
// is a copy; nil for any other chain.
func BitcoinFamilyParams(chainID string, testnet bool) *chaincfg.Params {
	if !models.IsBitcoinFamilyChainID(chainID) {
		return nil
	}
	testnet = testnet || chainID == models.ChainTBTC || chainID == models.ChainTLTC
	var params chaincfg.Params
	switch {
	case models.IsLitecoinChainID(chainID) && testnet:
		params = litecoinTestNetParams
	case models.IsLitecoinChainID(chainID):
		params = litecoinMainNetParams
	case testnet:
		params = chaincfg.TestNet3Params
	default:
		params = chaincfg.MainNetParams
	}
	return &params
}
