// Package addressing derives on-chain deposit addresses from MPC-produced
// public keys. Split out of app/services/wallet so that seeds and other
// tooling can build real wallet material without pulling the full wallet
// service (which would create an import cycle with tests/testutil → seeds).
package addressing

import (
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/bech32"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/mr-tron/base58"

	"github.com/macrowallets/waas/app/models"
)

// Bech32 human-readable parts of native SegWit addresses.
const (
	BtcHRPMainnet = "bc"
	BtcHRPTestnet = "tb"
	LtcHRPMainnet = "ltc"
	LtcHRPTestnet = "tltc"
)

// BtcHRP is the bech32 prefix of the Bitcoin network: tb on a test network, bc otherwise.
func BtcHRP(testnet bool) string {
	if testnet {
		return BtcHRPTestnet
	}
	return BtcHRPMainnet
}

// LtcHRP is the bech32 prefix of the Litecoin network: tltc on a test network, ltc otherwise.
func LtcHRP(testnet bool) string {
	if testnet {
		return LtcHRPTestnet
	}
	return LtcHRPMainnet
}

// UTXOHRP is the bech32 prefix of the Bitcoin-family network chainID transacts on.
func UTXOHRP(chainID string, testnet bool) string {
	if models.IsLitecoinChainID(chainID) {
		return LtcHRP(testnet)
	}
	return BtcHRP(testnet)
}

// DeriveAddressOnNetwork is DeriveAddress for a chain record that may point at a
// test network under a mainnet id: a "btc" record on testnet derives tb1 addresses
// and an "ltc" one tltc1. The address of the other chains does not depend on the
// network.
func DeriveAddressOnNetwork(chainID string, testnet bool, pubKey []byte) (string, error) {
	switch chainID {
	case models.ChainBTC, models.ChainLTC:
		return DeriveBtcAddress(UTXOHRP(chainID, testnet), pubKey)
	}
	return DeriveAddress(chainID, pubKey)
}

// DeriveAddress derives the on-chain deposit address for the MPC combined
// public key. secp256k1 chains take the 33-byte compressed SEC pubkey;
// Solana chains take a raw 32-byte ed25519 pubkey.
func DeriveAddress(chainID string, pubKey []byte) (string, error) {
	if models.IsEVMChainID(chainID) {
		return DeriveEthAddress(pubKey)
	}
	switch chainID {
	case models.ChainBTC:
		return DeriveBtcAddress(BtcHRPMainnet, pubKey)
	case models.ChainTBTC:
		return DeriveBtcAddress(BtcHRPTestnet, pubKey)
	case models.ChainLTC:
		return DeriveBtcAddress(LtcHRPMainnet, pubKey)
	case models.ChainTLTC:
		return DeriveBtcAddress(LtcHRPTestnet, pubKey)
	case models.ChainTron, models.ChainTTron:
		return DeriveTronAddress(pubKey)
	case models.ChainXRP, models.ChainTXRP:
		return DeriveXRPAddress(pubKey)
	case models.ChainSOL, models.ChainTSOL:
		return DeriveSolAddress(pubKey)
	default:
		return "", fmt.Errorf("unsupported chain for address derivation: %s", chainID)
	}
}

func DeriveEthAddress(compressedPubKey []byte) (string, error) {
	pub, err := btcec.ParsePubKey(compressedPubKey)
	if err != nil {
		return "", fmt.Errorf("parse pubkey: %w", err)
	}
	uncompressed := pub.SerializeUncompressed()[1:]
	hash := crypto.Keccak256(uncompressed)
	return "0x" + hex.EncodeToString(hash[12:]), nil
}

// DeriveBtcAddress derives a native SegWit (P2WPKH / bech32) address.
// hrp is "bc"/"tb" for Bitcoin mainnet/testnet, "ltc"/"tltc" for Litecoin.
func DeriveBtcAddress(hrp string, compressedPubKey []byte) (string, error) {
	pub, err := btcec.ParsePubKey(compressedPubKey)
	if err != nil {
		return "", fmt.Errorf("parse pubkey: %w", err)
	}
	pubHash := btcutil.Hash160(pub.SerializeCompressed())

	conv, err := bech32.ConvertBits(pubHash, 8, 5, true)
	if err != nil {
		return "", fmt.Errorf("convert bits: %w", err)
	}
	witnessProgram := append([]byte{0x00}, conv...)
	addr, err := bech32.Encode(hrp, witnessProgram)
	if err != nil {
		return "", fmt.Errorf("bech32 encode: %w", err)
	}
	return addr, nil
}

func DeriveSolAddress(pubKey []byte) (string, error) {
	if len(pubKey) != 32 {
		return "", fmt.Errorf("ed25519 pubkey must be 32 bytes, got %d", len(pubKey))
	}
	return base58.Encode(pubKey), nil
}
