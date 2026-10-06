package addressing

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/mr-tron/base58"
)

// TRON addresses are the last 20 bytes of keccak256(uncompressed public key), like
// EVM, prefixed with 0x41 and Base58Check encoded (T...). Mainnet and the Nile and
// Shasta testnets use the same encoding.
const (
	TronAddressPrefix       byte = 0x41
	TronAddressBodySize          = 20
	TronAddressSize              = 1 + TronAddressBodySize
	tronChecksumSize             = 4
	tronBase58AddressSize        = 34
	tronHexAddressSize           = 2 * TronAddressSize
	tronBase58AddressLead        = "T"
	ethAddressHexPrefix          = "0x"
	ethAddressBodyHexLength      = 2 * TronAddressBodySize
)

// DeriveTronAddress derives the Base58Check TRON address of a 33-byte compressed
// secp256k1 public key.
func DeriveTronAddress(compressedPubKey []byte) (string, error) {
	pub, err := btcec.ParsePubKey(compressedPubKey)
	if err != nil {
		return "", fmt.Errorf("parse pubkey: %w", err)
	}
	hash := crypto.Keccak256(pub.SerializeUncompressed()[1:])
	raw := make([]byte, 0, TronAddressSize)
	raw = append(raw, TronAddressPrefix)
	raw = append(raw, hash[len(hash)-TronAddressBodySize:]...)
	return EncodeTronAddress(raw)
}

// EncodeTronAddress Base58Check-encodes a 21-byte 0x41-prefixed address.
func EncodeTronAddress(raw []byte) (string, error) {
	if err := requireTronRawAddress(raw); err != nil {
		return "", err
	}
	payload := make([]byte, 0, TronAddressSize+tronChecksumSize)
	payload = append(payload, raw...)
	payload = append(payload, tronChecksum(raw)...)
	return base58.Encode(payload), nil
}

// DecodeTronAddress returns the 21-byte 0x41-prefixed form of a Base58Check TRON
// address, failing on any other length, prefix or checksum.
func DecodeTronAddress(address string) ([]byte, error) {
	trimmed := strings.TrimSpace(address)
	if len(trimmed) != tronBase58AddressSize || !strings.HasPrefix(trimmed, tronBase58AddressLead) {
		return nil, fmt.Errorf("tron address %q is not a %d-character T... address", address, tronBase58AddressSize)
	}
	decoded, err := base58.Decode(trimmed)
	if err != nil {
		return nil, fmt.Errorf("tron address %q is not base58: %w", address, err)
	}
	if len(decoded) != TronAddressSize+tronChecksumSize {
		return nil, fmt.Errorf("tron address %q decodes to %d bytes, want %d", address, len(decoded), TronAddressSize+tronChecksumSize)
	}
	raw, checksum := decoded[:TronAddressSize], decoded[TronAddressSize:]
	if err := requireTronRawAddress(raw); err != nil {
		return nil, err
	}
	if !bytes.Equal(checksum, tronChecksum(raw)) {
		return nil, fmt.Errorf("tron address %q has a bad checksum", address)
	}
	return append([]byte(nil), raw...), nil
}

// IsTronAddress reports whether address is a valid Base58Check TRON address.
func IsTronAddress(address string) bool {
	_, err := DecodeTronAddress(address)
	return err == nil
}

// TronAddressToHex is the 42-character hex form (41...) the TRON HTTP API accepts
// with visible=false.
func TronAddressToHex(address string) (string, error) {
	raw, err := DecodeTronAddress(address)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// TronAddressFromHex accepts the 41-prefixed hex form (as returned by the TRON API
// and in protobuf fields) or a 0x-prefixed 20-byte EVM-style hex body (as in
// TRC-20 event topics and ABI words) and returns the Base58Check address.
func TronAddressFromHex(value string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.HasPrefix(trimmed, ethAddressHexPrefix) && len(trimmed) == len(ethAddressHexPrefix)+ethAddressBodyHexLength:
		body, err := hex.DecodeString(trimmed[len(ethAddressHexPrefix):])
		if err != nil {
			return "", fmt.Errorf("tron hex address %q: %w", value, err)
		}
		return EncodeTronAddress(append([]byte{TronAddressPrefix}, body...))
	case len(trimmed) == tronHexAddressSize:
		raw, err := hex.DecodeString(trimmed)
		if err != nil {
			return "", fmt.Errorf("tron hex address %q: %w", value, err)
		}
		return EncodeTronAddress(raw)
	default:
		return "", fmt.Errorf("tron hex address %q must be %d hex characters (41...) or 0x plus %d", value, tronHexAddressSize, ethAddressBodyHexLength)
	}
}

func requireTronRawAddress(raw []byte) error {
	if len(raw) != TronAddressSize {
		return fmt.Errorf("tron address must be %d bytes, got %d", TronAddressSize, len(raw))
	}
	if raw[0] != TronAddressPrefix {
		return fmt.Errorf("tron address must start with 0x%x, got 0x%x", TronAddressPrefix, raw[0])
	}
	return nil
}

func tronChecksum(raw []byte) []byte {
	first := sha256.Sum256(raw)
	second := sha256.Sum256(first[:])
	return second[:tronChecksumSize]
}
