package chain

import (
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"

	"github.com/macrowallets/waas/app/services/addressing"
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
// The adapter decodes its own bech32 addresses (witnessProgram) instead.
var (
	ltcMainNetParams = chaincfg.Params{
		Name:             ltcMainNetParamsName,
		Net:              ltcMainNetMagic,
		DefaultPort:      ltcMainNetPort,
		Bech32HRPSegwit:  addressing.LtcHRPMainnet,
		PubKeyHashAddrID: ltcMainNetPubKeyHashID,
		ScriptHashAddrID: ltcMainNetScriptHashID,
		PrivateKeyID:     ltcMainNetPrivateKeyID,
		HDPrivateKeyID:   ltcMainNetHDPrivateKeyID,
		HDPublicKeyID:    ltcMainNetHDPublicKeyID,
		HDCoinType:       ltcMainNetSLIP44CoinType,
	}
	ltcTestNetParams = chaincfg.Params{
		Name:             ltcTestNetParamsName,
		Net:              ltcTestNetMagic,
		DefaultPort:      ltcTestNetPort,
		Bech32HRPSegwit:  addressing.LtcHRPTestnet,
		PubKeyHashAddrID: ltcTestNetPubKeyHashID,
		ScriptHashAddrID: ltcTestNetScriptHashID,
		PrivateKeyID:     ltcTestNetPrivateKeyID,
		HDPrivateKeyID:   ltcTestNetHDPrivateKeyID,
		HDPublicKeyID:    ltcTestNetHDPublicKeyID,
		HDCoinType:       ltcTestNetSLIP44CoinType,
	}
)

// Litecoin Core policy (src/policy/policy.h at the same commit): DUST_RELAY_TX_FEE is
// 30,000 sat/kvB, ten times Bitcoin's 3,000, while DEFAULT_MIN_RELAY_TX_FEE
// (src/validation.h) is 1,000 sat/kvB = 1 sat/vB, as in Bitcoin. GetDustThreshold
// (src/policy/policy.cpp) charges an output its own size plus the input that spends
// it: 98 vB for P2WPKH (2,940 sats) and 182 B for P2PKH (5,460 sats). Like Bitcoin's
// 546, the limit used is the P2PKH one, which every standard output clears.
const ltcDustSats int64 = 5_460
