package keyexport

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"

	"filippo.io/edwards25519"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/mr-tron/base58"
)

func randomSecp256k1Key(t *testing.T) []byte {
	t.Helper()
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return key.Serialize()
}

func TestEVMPrivateKeyHex_ImportsToTheSameAddressAsGoEthereum(t *testing.T) {
	privateKey := randomSecp256k1Key(t)
	exported, err := EVMPrivateKeyHex(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(exported, "0x") || len(exported) != 66 {
		t.Fatalf("EVM key must be 0x + 64 hex, got %d chars", len(exported))
	}
	ecdsaKey, err := crypto.HexToECDSA(strings.TrimPrefix(exported, "0x"))
	if err != nil {
		t.Fatal(err)
	}
	want := crypto.PubkeyToAddress(ecdsaKey.PublicKey).Hex()
	got, err := EVMAddressOfPrivateKeyHex(exported)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(got, want) {
		t.Fatalf("address %s, go-ethereum derives %s", got, want)
	}
	if _, err := EVMPrivateKeyHex(privateKey[:31]); err == nil {
		t.Fatal("a 31-byte key must be refused")
	}
}

func TestBitcoinWIF_EncodesTestnetAndMainnetCompressed(t *testing.T) {
	privateKey := randomSecp256k1Key(t)
	cases := []struct {
		testnet  bool
		params   *chaincfg.Params
		prefixes string
	}{
		{testnet: true, params: &chaincfg.TestNet3Params, prefixes: "c"},
		{testnet: false, params: &chaincfg.MainNetParams, prefixes: "KL"},
	}
	for _, tc := range cases {
		key, err := NewBitcoinKey(privateKey, tc.testnet)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.ContainsAny(key.WIF[:1], tc.prefixes) {
			t.Fatalf("testnet=%t: compressed WIF starts with %q", tc.testnet, key.WIF[:1])
		}
		decoded, err := btcutil.DecodeWIF(key.WIF)
		if err != nil {
			t.Fatal(err)
		}
		if !decoded.IsForNet(tc.params) || !decoded.CompressPubKey || !bytes.Equal(decoded.PrivKey.Serialize(), privateKey) {
			t.Fatalf("testnet=%t: WIF does not decode to the compressed key on its network", tc.testnet)
		}
		witness, err := btcutil.NewAddressWitnessPubKeyHash(btcutil.Hash160(decoded.SerializePubKey()), tc.params)
		if err != nil {
			t.Fatal(err)
		}
		got, err := BitcoinP2WPKHAddressOfWIF(key.WIF, tc.testnet)
		if err != nil || got != witness.EncodeAddress() {
			t.Fatalf("testnet=%t: address %s err %v, btcutil derives %s", tc.testnet, got, err, witness.EncodeAddress())
		}
		if _, err := BitcoinP2WPKHAddressOfWIF(key.WIF, !tc.testnet); err == nil {
			t.Fatalf("testnet=%t: a WIF of one network must not verify on the other", tc.testnet)
		}
		if key.ElectrumImport != "p2wpkh:"+key.WIF || key.Descriptor != "wpkh("+key.WIF+")" {
			t.Fatalf("electrum %q descriptor %q", key.ElectrumImport, key.Descriptor)
		}
		checksum, _ := DescriptorChecksum(key.Descriptor)
		if key.DescriptorWithChecksum != key.Descriptor+"#"+checksum {
			t.Fatalf("descriptor checksum missing")
		}
	}
}

func TestDescriptorChecksum_MatchesBIP380(t *testing.T) {
	vectors := map[string]string{
		"raw(deadbeef)":            "89f8spxm",
		"wpkh(cTestVectorNotAKey)": "4u7gh8gd",
	}
	for descriptor, want := range vectors {
		got, err := DescriptorChecksum(descriptor)
		if err != nil || got != want {
			t.Fatalf("checksum(%s) = %s, %v; want %s", descriptor, got, err, want)
		}
	}
	if _, err := DescriptorChecksum("wpkh(é)"); err == nil {
		t.Fatal("characters outside the BIP-380 charset must be refused")
	}
}

func TestSolanaKeypair_Base58AndJSONImportToTheSeedAddress(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	publicKey := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	want := base58.Encode(publicKey)

	key, err := NewSolanaKey(seed, "solana-keygen/index-1.json")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base58.Decode(key.KeypairBase58)
	if err != nil || len(decoded) != ed25519.PrivateKeySize || !bytes.Equal(decoded[:32], seed) || !bytes.Equal(decoded[32:], publicKey) {
		t.Fatal("base58 secret must be seed || public key (64 bytes)")
	}
	if key.SeedHex != hex.EncodeToString(seed) || len(key.KeypairJSON) != ed25519.PrivateKeySize {
		t.Fatal("seed hex or json array is wrong")
	}
	fromBase58, err := SolanaAddressOfKeypairBase58(key.KeypairBase58)
	if err != nil || fromBase58 != want {
		t.Fatalf("base58 keypair → %s %v, want %s", fromBase58, err, want)
	}
	fromJSON, err := SolanaAddressOfKeypairJSON(key.KeypairJSON)
	if err != nil || fromJSON != want {
		t.Fatalf("json keypair → %s %v, want %s", fromJSON, err, want)
	}

	tampered := append([]byte{}, decoded...)
	tampered[40] ^= 0xff
	if _, err := SolanaAddressOfKeypairBase58(base58.Encode(tampered)); err == nil {
		t.Fatal("a keypair whose public half does not match its seed must be refused")
	}
	if _, err := SolanaAddressOfKeypairJSON([]int{256}); err == nil {
		t.Fatal("json values outside 0..255 must be refused")
	}
}

func randomEd25519ScalarBigEndian(t *testing.T) []byte {
	t.Helper()
	wide := make([]byte, 64)
	if _, err := rand.Read(wide); err != nil {
		t.Fatal(err)
	}
	scalar, err := edwards25519.NewScalar().SetUniformBytes(wide)
	if err != nil {
		t.Fatal(err)
	}
	return reversed(scalar.Bytes())
}

// The genesis key is a scalar: read as an RFC 8032 seed (what Phantom and
// solana-keygen do with 32 bytes) it yields another public key.
func TestSolanaScalar_RederivesThePublicKeyButIsNotASeed(t *testing.T) {
	scalar := randomEd25519ScalarBigEndian(t)
	publicKey, err := Ed25519PublicKeyOfScalar(scalar)
	if err != nil {
		t.Fatal(err)
	}
	key, err := NewSolanaScalarKey(scalar)
	if err != nil {
		t.Fatal(err)
	}
	if key.ImportableInPhantom || key.ScalarHexLittleEndian != hex.EncodeToString(reversed(scalar)) {
		t.Fatalf("scalar key %+v", key.ImportableInPhantom)
	}
	address, err := SolanaAddressOfScalarHex(key.ScalarHexBigEndian)
	if err != nil || address != base58.Encode(publicKey) {
		t.Fatalf("scalar → %s %v", address, err)
	}
	for _, candidate := range [][]byte{scalar, reversed(scalar)} {
		asSeed := ed25519.NewKeyFromSeed(candidate).Public().(ed25519.PublicKey)
		if bytes.Equal(asSeed, publicKey) {
			t.Fatal("the scalar must not work as an RFC 8032 seed")
		}
	}
	if _, err := Ed25519PublicKeyOfScalar(make([]byte, 32)); err == nil {
		t.Fatal("the zero scalar must be refused")
	}
}
