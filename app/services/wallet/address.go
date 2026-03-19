package wallet

import (
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/bech32"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/mr-tron/base58"
)

// deriveAddress derives the single MPC deposit address from the combined public key.
func deriveAddress(chainID string, compressedPubKey []byte) (string, error) {
	switch chainID {
	case "eth", "polygon":
		return deriveEthAddress(compressedPubKey)
	case "btc":
		return deriveBtcAddress(compressedPubKey)
	case "sol":
		return deriveSolAddress(compressedPubKey)
	default:
		return "", fmt.Errorf("unsupported chain for address derivation: %s", chainID)
	}
}

func deriveEthAddress(compressedPubKey []byte) (string, error) {
	pub, err := btcec.ParsePubKey(compressedPubKey)
	if err != nil {
		return "", fmt.Errorf("parse pubkey: %w", err)
	}
	// go-ethereum expects uncompressed pubkey without 0x04 prefix
	uncompressed := pub.SerializeUncompressed()[1:]
	hash := crypto.Keccak256(uncompressed)
	return "0x" + hex.EncodeToString(hash[12:]), nil
}

// deriveBtcAddress derives a native SegWit (P2WPKH / bech32) mainnet address
// without importing chaincfg to avoid btcd module version conflicts.
func deriveBtcAddress(compressedPubKey []byte) (string, error) {
	pub, err := btcec.ParsePubKey(compressedPubKey)
	if err != nil {
		return "", fmt.Errorf("parse pubkey: %w", err)
	}
	// HASH160 = RIPEMD160(SHA256(pubkey))
	pubHash := btcutil.Hash160(pub.SerializeCompressed())

	// P2WPKH witness program: version 0, 20-byte pubkey hash
	// Bech32 encoding: hrp "bc" for mainnet, witness version 0 prepended
	conv, err := bech32.ConvertBits(pubHash, 8, 5, true)
	if err != nil {
		return "", fmt.Errorf("convert bits: %w", err)
	}
	// Prepend witness version 0
	witnessProgram := append([]byte{0x00}, conv...)
	addr, err := bech32.Encode("bc", witnessProgram)
	if err != nil {
		return "", fmt.Errorf("bech32 encode: %w", err)
	}
	return addr, nil
}

func deriveSolAddress(pubKey []byte) (string, error) {
	if len(pubKey) != 32 {
		return "", fmt.Errorf("ed25519 pubkey must be 32 bytes, got %d", len(pubKey))
	}
	return base58.Encode(pubKey), nil
}
