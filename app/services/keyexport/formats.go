package keyexport

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"filippo.io/edwards25519"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/mr-tron/base58"

	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/app/services/hdkey"
)

const (
	evmHexPrefix         = "0x"
	electrumP2WPKHPrefix = "p2wpkh:"
	ed25519ScalarSize    = 32
)

var (
	errBadPrivateKey    = errors.New("private key must be 32 bytes")
	errBadEd25519Seed   = errors.New("ed25519 seed must be 32 bytes")
	errBadEd25519Scalar = errors.New("ed25519 scalar must be a canonical non-zero 32-byte scalar")
)

// bitcoinParams picks the WIF version: every Bitcoin test network (testnet3,
// testnet4, signet, regtest) shares the testnet WIF prefix.
func bitcoinParams(testnet bool) *chaincfg.Params {
	if testnet {
		return &chaincfg.TestNet3Params
	}
	return &chaincfg.MainNetParams
}

// EVMPrivateKeyHex is the 0x-prefixed hex MetaMask imports.
func EVMPrivateKeyHex(privateKey []byte) (string, error) {
	if len(privateKey) != hdkey.PrivateKeySize {
		return "", errBadPrivateKey
	}
	return evmHexPrefix + hex.EncodeToString(privateKey), nil
}

// EVMAddressOfPrivateKeyHex re-derives the address from the exported hex form.
func EVMAddressOfPrivateKeyHex(privateKeyHex string) (string, error) {
	raw, err := hex.DecodeString(strings.TrimPrefix(privateKeyHex, evmHexPrefix))
	if err != nil || len(raw) != hdkey.PrivateKeySize {
		return "", errBadPrivateKey
	}
	defer zeroBytes(raw)
	publicKey, err := hdkey.PublicKeyOf(raw)
	if err != nil {
		return "", err
	}
	return addressing.DeriveEthAddress(publicKey)
}

// BitcoinWIF is the compressed WIF of privateKey on mainnet or on a test network.
func BitcoinWIF(privateKey []byte, testnet bool) (string, error) {
	if len(privateKey) != hdkey.PrivateKeySize {
		return "", errBadPrivateKey
	}
	key, _ := btcec.PrivKeyFromBytes(privateKey)
	defer key.Zero()
	wif, err := btcutil.NewWIF(key, bitcoinParams(testnet), true)
	if err != nil {
		return "", fmt.Errorf("encode wif: %w", err)
	}
	return wif.String(), nil
}

// BitcoinP2WPKHAddressOfWIF decodes a WIF, checks its network and compression, and
// returns the native SegWit address it controls.
func BitcoinP2WPKHAddressOfWIF(wifString string, testnet bool) (string, error) {
	wif, err := btcutil.DecodeWIF(wifString)
	if err != nil {
		return "", fmt.Errorf("decode wif: %w", err)
	}
	defer wif.PrivKey.Zero()
	if !wif.IsForNet(bitcoinParams(testnet)) {
		return "", errors.New("wif is for another bitcoin network")
	}
	if !wif.CompressPubKey {
		return "", errors.New("wif is not compressed")
	}
	return addressing.DeriveBtcAddress(addressing.BtcHRP(testnet), wif.SerializePubKey())
}

// NewBitcoinKey renders every Bitcoin form of a key: WIF, Electrum import line and
// the Bitcoin Core / Sparrow wpkh descriptor with its checksum.
func NewBitcoinKey(privateKey []byte, testnet bool) (*BitcoinKey, error) {
	wif, err := BitcoinWIF(privateKey, testnet)
	if err != nil {
		return nil, err
	}
	descriptor := "wpkh(" + wif + ")"
	checksum, err := DescriptorChecksum(descriptor)
	if err != nil {
		return nil, err
	}
	return &BitcoinKey{
		WIF:                    wif,
		ElectrumImport:         electrumP2WPKHPrefix + wif,
		Descriptor:             descriptor,
		DescriptorWithChecksum: descriptor + "#" + checksum,
		PrivateKeyHex:          hex.EncodeToString(privateKey),
	}, nil
}

// SolanaKeypair is seed || public key, the 64-byte secret Phantom (base58) and
// solana-keygen (JSON byte array) import. The caller must zero it.
func SolanaKeypair(seed []byte) ([]byte, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, errBadEd25519Seed
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// NewSolanaKey renders the Phantom and solana-keygen forms of an RFC 8032 seed.
func NewSolanaKey(seed []byte, keypairFile string) (*SolanaKey, error) {
	keypair, err := SolanaKeypair(seed)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(keypair)
	asJSON := make([]int, len(keypair))
	for i, b := range keypair {
		asJSON[i] = int(b)
	}
	return &SolanaKey{
		SeedHex:       hex.EncodeToString(seed),
		KeypairBase58: base58.Encode(keypair),
		KeypairJSON:   asJSON,
		KeypairFile:   keypairFile,
	}, nil
}

// SolanaAddressOfKeypairBase58 checks that the 64-byte keypair is consistent (its
// second half is the public key of its seed) and returns its address.
func SolanaAddressOfKeypairBase58(encoded string) (string, error) {
	keypair, err := base58.Decode(encoded)
	if err != nil {
		return "", fmt.Errorf("decode base58 keypair")
	}
	defer zeroBytes(keypair)
	return solanaAddressOfKeypair(keypair)
}

// SolanaAddressOfKeypairJSON does the same for the solana-keygen JSON byte array.
func SolanaAddressOfKeypairJSON(values []int) (string, error) {
	keypair := make([]byte, len(values))
	defer zeroBytes(keypair)
	for i, v := range values {
		if v < 0 || v > 0xff {
			return "", errors.New("keypair json holds a value outside 0..255")
		}
		keypair[i] = byte(v)
	}
	return solanaAddressOfKeypair(keypair)
}

func solanaAddressOfKeypair(keypair []byte) (string, error) {
	if len(keypair) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("keypair must be %d bytes, got %d", ed25519.PrivateKeySize, len(keypair))
	}
	rebuilt := ed25519.NewKeyFromSeed(keypair[:ed25519.SeedSize])
	defer zeroBytes(rebuilt)
	publicKey := rebuilt.Public().(ed25519.PublicKey)
	if string(publicKey) != string(keypair[ed25519.SeedSize:]) {
		return "", errors.New("keypair public half does not belong to its seed")
	}
	return addressing.DeriveSolAddress(publicKey)
}

// NewSolanaScalarKey renders a raw ed25519 scalar (genesis key of an MPC wallet) in
// both byte orders: big-endian as the backend reconstructs it, little-endian as
// RFC 8032 and most curve libraries encode scalars.
func NewSolanaScalarKey(scalarBigEndian []byte) (*SolanaScalarKey, error) {
	if len(scalarBigEndian) != ed25519ScalarSize {
		return nil, errBadEd25519Scalar
	}
	littleEndian := reversed(scalarBigEndian)
	defer zeroBytes(littleEndian)
	return &SolanaScalarKey{
		ScalarHexBigEndian:    hex.EncodeToString(scalarBigEndian),
		ScalarHexLittleEndian: hex.EncodeToString(littleEndian),
		ImportableInPhantom:   false,
	}, nil
}

// Ed25519PublicKeyOfScalar returns scalar·B for a big-endian scalar.
func Ed25519PublicKeyOfScalar(scalarBigEndian []byte) ([]byte, error) {
	if len(scalarBigEndian) != ed25519ScalarSize {
		return nil, errBadEd25519Scalar
	}
	littleEndian := reversed(scalarBigEndian)
	defer zeroBytes(littleEndian)
	scalar, err := edwards25519.NewScalar().SetCanonicalBytes(littleEndian)
	if err != nil {
		return nil, errBadEd25519Scalar
	}
	defer scalar.Set(edwards25519.NewScalar())
	if scalar.Equal(edwards25519.NewScalar()) == 1 {
		return nil, errBadEd25519Scalar
	}
	return new(edwards25519.Point).ScalarBaseMult(scalar).Bytes(), nil
}

// SolanaAddressOfScalarHex re-derives the address from the exported big-endian hex.
func SolanaAddressOfScalarHex(scalarHexBigEndian string) (string, error) {
	raw, err := hex.DecodeString(scalarHexBigEndian)
	if err != nil {
		return "", errBadEd25519Scalar
	}
	defer zeroBytes(raw)
	publicKey, err := Ed25519PublicKeyOfScalar(raw)
	if err != nil {
		return "", err
	}
	return addressing.DeriveSolAddress(publicKey)
}

func reversed(in []byte) []byte {
	out := make([]byte, len(in))
	for i := range in {
		out[i] = in[len(in)-1-i]
	}
	return out
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
