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

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/hdkey"
)

const (
	evmHexPrefix         = "0x"
	electrumP2WPKHPrefix = "p2wpkh:"
	ed25519ScalarSize    = 32
	tronPrivateKeyHexLen = 2 * hdkey.PrivateKeySize
)

var (
	errBadPrivateKey    = errors.New("private key must be 32 bytes")
	errBadTronKeyHex    = errors.New("tron private key must be 64 hex characters without a 0x prefix")
	errBadEd25519Seed   = errors.New("ed25519 seed must be 32 bytes")
	errBadEd25519Scalar = errors.New("ed25519 scalar must be a canonical non-zero 32-byte scalar")
)

// utxoFamily is the mainnet id of the Bitcoin-family chain of chainID (btc or ltc);
// which of its networks a key is for is then decided by testnet alone.
func utxoFamily(chainID string) string {
	if models.IsLitecoinChainID(chainID) {
		return models.ChainLTC
	}
	return models.ChainBTC
}

// utxoParams picks the WIF version and bech32 prefix: Bitcoin 0x80/0xef with bc/tb
// (every Bitcoin test network shares the testnet prefixes), Litecoin 0xb0/0xef with
// ltc/tltc.
func utxoParams(chainID string, testnet bool) (*chaincfg.Params, error) {
	if !models.IsBitcoinFamilyChainID(chainID) {
		return nil, fmt.Errorf("chain %q has no Bitcoin-family key format", chainID)
	}
	params := chain.BitcoinFamilyParams(utxoFamily(chainID), testnet)
	if params == nil {
		return nil, fmt.Errorf("chain %q has no Bitcoin-family key format", chainID)
	}
	return params, nil
}

func utxoNetworkName(chainID string, testnet bool) string {
	switch {
	case models.IsLitecoinChainID(chainID) && testnet:
		return models.NetworkLitecoinTestnet
	case models.IsLitecoinChainID(chainID):
		return models.NetworkLitecoinMainnet
	case testnet:
		return models.NetworkBitcoinTestnet
	default:
		return models.NetworkBitcoinMainnet
	}
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

// BitcoinWIF is the compressed WIF of privateKey on Bitcoin mainnet or a test network.
func BitcoinWIF(privateKey []byte, testnet bool) (string, error) {
	return UTXOWIF(privateKey, models.ChainBTC, testnet)
}

// BitcoinP2WPKHAddressOfWIF is UTXOP2WPKHAddressOfWIF on Bitcoin.
func BitcoinP2WPKHAddressOfWIF(wifString string, testnet bool) (string, error) {
	return UTXOP2WPKHAddressOfWIF(wifString, models.ChainBTC, testnet)
}

// NewBitcoinKey is NewUTXOKey on Bitcoin.
func NewBitcoinKey(privateKey []byte, testnet bool) (*BitcoinKey, error) {
	return NewUTXOKey(privateKey, models.ChainBTC, testnet)
}

// UTXOWIF is the compressed WIF of privateKey on the Bitcoin-family network of
// chainID (Bitcoin or Litecoin) and testnet.
func UTXOWIF(privateKey []byte, chainID string, testnet bool) (string, error) {
	if len(privateKey) != hdkey.PrivateKeySize {
		return "", errBadPrivateKey
	}
	params, err := utxoParams(chainID, testnet)
	if err != nil {
		return "", err
	}
	key, _ := btcec.PrivKeyFromBytes(privateKey)
	defer key.Zero()
	wif, err := btcutil.NewWIF(key, params, true)
	if err != nil {
		return "", fmt.Errorf("encode wif: %w", err)
	}
	return wif.String(), nil
}

// UTXOP2WPKHAddressOfWIF decodes a WIF, checks its network and compression, and
// returns the native SegWit address it controls on that network. Bitcoin and
// Litecoin testnets share the WIF prefix 0xef, so the address (tb1 vs tltc1) is what
// tells them apart.
func UTXOP2WPKHAddressOfWIF(wifString, chainID string, testnet bool) (string, error) {
	params, err := utxoParams(chainID, testnet)
	if err != nil {
		return "", err
	}
	wif, err := btcutil.DecodeWIF(wifString)
	if err != nil {
		return "", fmt.Errorf("decode wif: %w", err)
	}
	defer wif.PrivKey.Zero()
	if !wif.IsForNet(params) {
		return "", fmt.Errorf("wif is not for %s", utxoNetworkName(chainID, testnet))
	}
	if !wif.CompressPubKey {
		return "", errors.New("wif is not compressed")
	}
	return addressing.DeriveBtcAddress(params.Bech32HRPSegwit, wif.SerializePubKey())
}

// NewUTXOKey renders every Bitcoin-family form of a key: WIF, the Electrum /
// Electrum-LTC import line and the Bitcoin Core / Litecoin Core / Sparrow wpkh
// descriptor with its checksum.
func NewUTXOKey(privateKey []byte, chainID string, testnet bool) (*BitcoinKey, error) {
	wif, err := UTXOWIF(privateKey, chainID, testnet)
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

// TronPrivateKeyHex is the 64-hex-character key, without 0x, that TronLink ("Import
// private key") and TronWeb import.
func TronPrivateKeyHex(privateKey []byte) (string, error) {
	if len(privateKey) != hdkey.PrivateKeySize {
		return "", errBadPrivateKey
	}
	return hex.EncodeToString(privateKey), nil
}

// TronAddressOfPrivateKeyHex re-derives the base58 T... address from the exported hex.
func TronAddressOfPrivateKeyHex(privateKeyHex string) (string, error) {
	if len(privateKeyHex) != tronPrivateKeyHexLen {
		return "", errBadTronKeyHex
	}
	raw, err := hex.DecodeString(privateKeyHex)
	if err != nil {
		return "", errBadTronKeyHex
	}
	defer zeroBytes(raw)
	publicKey, err := hdkey.PublicKeyOf(raw)
	if err != nil {
		return "", err
	}
	return addressing.DeriveTronAddress(publicKey)
}

// NewTronKey renders the TronLink form of a key and the hex (41...) form of the
// address it re-derives.
func NewTronKey(privateKey []byte) (*TronKey, error) {
	privateKeyHex, err := TronPrivateKeyHex(privateKey)
	if err != nil {
		return nil, err
	}
	address, err := TronAddressOfPrivateKeyHex(privateKeyHex)
	if err != nil {
		return nil, err
	}
	addressHex, err := addressing.TronAddressToHex(address)
	if err != nil {
		return nil, err
	}
	return &TronKey{PrivateKeyHex: privateKeyHex, AddressHex: addressHex}, nil
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
