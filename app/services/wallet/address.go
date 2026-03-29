package wallet

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

// deriveAddress derives the single MPC deposit address from the combined public key.
func deriveAddress(chainID string, compressedPubKey []byte) (string, error) {
	switch chainID {
	case models.ChainETH, models.ChainPolygon, models.ChainTETH, models.ChainTPolygon:
		return deriveEthAddress(compressedPubKey)
	case models.ChainBTC:
		return deriveBtcAddress("bc", compressedPubKey)
	case models.ChainTBTC:
		return deriveBtcAddress("tb", compressedPubKey)
	case models.ChainSOL, models.ChainTSOL:
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

// deriveBtcAddress derives a native SegWit (P2WPKH / bech32) address.
// hrp is "bc" for mainnet, "tb" for testnet.
func deriveBtcAddress(hrp string, compressedPubKey []byte) (string, error) {
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

func deriveSolAddress(pubKey []byte) (string, error) {
	if len(pubKey) != 32 {
		return "", fmt.Errorf("ed25519 pubkey must be 32 bytes, got %d", len(pubKey))
	}
	return base58.Encode(pubKey), nil
}
