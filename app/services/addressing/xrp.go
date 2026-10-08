package addressing

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math/big"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"golang.org/x/crypto/ripemd160"
)

// Classic XRP Ledger account addresses. The account id is
// RIPEMD160(SHA-256(compressed secp256k1 public key)), prefixed with 0x00 and
// Base58Check-encoded with the XRPL alphabet. The result starts with r.
// Mainnet and the altnet testnet use the same classic address.
const (
	xrpAddressVersion  byte = 0x00
	xrpAccountIDSize        = 20
	xrpPayloadSize          = 1 + xrpAccountIDSize
	xrpChecksumSize         = 4
	xrpAddressAlphabet      = "rpshnaf39wBUDNEGHJKLM4PQRST7VWXYZ2bcdeCg65jkm8oFqi1tuvAxyz"
)

// DeriveXRPAddress derives the classic r-address of a 33-byte compressed
// secp256k1 public key.
func DeriveXRPAddress(compressedPubKey []byte) (string, error) {
	pub, err := btcec.ParsePubKey(compressedPubKey)
	if err != nil {
		return "", fmt.Errorf("parse pubkey: %w", err)
	}
	accountID := xrpAccountID(pub.SerializeCompressed())
	payload := make([]byte, 0, xrpPayloadSize)
	payload = append(payload, xrpAddressVersion)
	payload = append(payload, accountID...)
	return encodeXRPClassicAddress(payload)
}

// IsXRPClassicAddress reports whether address is a classic XRPL account
// (r..., version 0x00, valid checksum). X-addresses are not classic accounts.
func IsXRPClassicAddress(address string) bool {
	_, err := DecodeXRPClassicAddress(address)
	return err == nil
}

// DecodeXRPClassicAddress returns the 20-byte account id of a classic address.
func DecodeXRPClassicAddress(address string) ([]byte, error) {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" || !strings.HasPrefix(trimmed, "r") {
		return nil, fmt.Errorf("xrp address %q is not a classic r-address", address)
	}
	decoded, err := decodeXRPBase58(trimmed)
	if err != nil {
		return nil, fmt.Errorf("xrp address %q is not xrpl base58: %w", address, err)
	}
	if len(decoded) != xrpPayloadSize+xrpChecksumSize {
		return nil, fmt.Errorf("xrp address %q decodes to %d bytes, want %d", address, len(decoded), xrpPayloadSize+xrpChecksumSize)
	}
	payload, checksum := decoded[:xrpPayloadSize], decoded[xrpPayloadSize:]
	if payload[0] != xrpAddressVersion {
		return nil, fmt.Errorf("xrp address %q is not an account address", address)
	}
	if !bytes.Equal(checksum, xrpChecksum(payload)) {
		return nil, fmt.Errorf("xrp address %q has a bad checksum", address)
	}
	return append([]byte(nil), payload[1:]...), nil
}

func encodeXRPClassicAddress(payload []byte) (string, error) {
	if len(payload) != xrpPayloadSize || payload[0] != xrpAddressVersion {
		return "", fmt.Errorf("xrp account payload must be %d bytes starting with %#x", xrpPayloadSize, xrpAddressVersion)
	}
	raw := make([]byte, 0, len(payload)+xrpChecksumSize)
	raw = append(raw, payload...)
	raw = append(raw, xrpChecksum(payload)...)
	return encodeXRPBase58(raw), nil
}

func xrpAccountID(compressedPubKey []byte) []byte {
	sum := sha256.Sum256(compressedPubKey)
	ripe := ripemd160.New()
	_, _ = ripe.Write(sum[:])
	return ripe.Sum(nil)
}

func xrpChecksum(payload []byte) []byte {
	first := sha256.Sum256(payload)
	second := sha256.Sum256(first[:])
	return second[:xrpChecksumSize]
}

func encodeXRPBase58(input []byte) string {
	zeros := 0
	for zeros < len(input) && input[zeros] == 0 {
		zeros++
	}
	value := new(big.Int).SetBytes(input)
	base := big.NewInt(58)
	mod := new(big.Int)
	encoded := make([]byte, 0, len(input)*2)
	for value.Sign() > 0 {
		value.DivMod(value, base, mod)
		encoded = append(encoded, xrpAddressAlphabet[mod.Int64()])
	}
	for i := 0; i < zeros; i++ {
		encoded = append(encoded, xrpAddressAlphabet[0])
	}
	for i, j := 0, len(encoded)-1; i < j; i, j = i+1, j-1 {
		encoded[i], encoded[j] = encoded[j], encoded[i]
	}
	return string(encoded)
}

func decodeXRPBase58(address string) ([]byte, error) {
	value := new(big.Int)
	base := big.NewInt(58)
	for _, r := range address {
		index := strings.IndexRune(xrpAddressAlphabet, r)
		if index < 0 {
			return nil, fmt.Errorf("character %q is outside the xrpl alphabet", r)
		}
		value.Mul(value, base)
		value.Add(value, big.NewInt(int64(index)))
	}
	decoded := value.Bytes()
	zeros := 0
	for zeros < len(address) && address[zeros] == xrpAddressAlphabet[0] {
		zeros++
	}
	out := make([]byte, zeros+len(decoded))
	copy(out[zeros:], decoded)
	return out, nil
}
