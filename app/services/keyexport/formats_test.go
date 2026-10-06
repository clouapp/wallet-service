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

	bitcoinchain "github.com/macrowallets/waas/app/adapters/chain/bitcoin"
	"github.com/macrowallets/waas/app/models"
)

func randomSecp256k1Key(t *testing.T) []byte {
	t.Helper()
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return key.Serialize()
}

func TestEVM_PrivateKeyHex_ImportsToTheSameAddressAsGoEthereum(t *testing.T) {
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

func TestBitcoin_WIF_EncodesTestnetAndMainnetCompressed(t *testing.T) {
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
		key, err := NewBitcoinKey(BitcoinKeyDeps{PrivateKey: privateKey, Testnet: tc.testnet})
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

// WIF vectors from litecoin-project/litecoin src/test/data/key_io_valid.json at commit
// ec1b6489a900d09cf5991e220dce089c77a232a2 (compressed keys, chain main / test).
const (
	litecoinMainnetWIF    = "T5MZ5z9WqJxzVxYyVPecTJUSDkzDWrUYe1JuSX2AqJ9jKmLJrvTE"
	litecoinMainnetWIFKey = "44b78d45adc801a65949661d5df1c4a44f532cd422be413a505d776784ddbe25"
	litecoinTestnetWIF    = "cQaeKQwuakynYD9iebyxsKiBKF8RT3G6zoqRNUDybMsAimANRypo"
	litecoinTestnetWIFKey = "597b8f070b98ee1f997fa3cb976466fa0e931256246b8c7177d2b067eed06ad7"

	keyOneLitecoinTestnetAddress = "tltc1qw508d6qejxtdg4y5r3zarvary0c5xw7klfsuq0"
	keyOneTronAddress            = "TMVQGm1qAQYVdetCeGRRkTWYYrLXuHK2HC"
	// 0x41 + the Ethereum address of private key 1 (0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf).
	keyOneTronAddressHex = "417e5f4552091a69125d5dfcb7b8c2659029395bdf"
)

func privateKeyOne() []byte {
	key := make([]byte, 32)
	key[31] = 1
	return key
}

func mustDecodeHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestUTXOWIF_EncodesLitecoinCoreVectors(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		testnet bool
		wif     string
		hrp     string
	}{
		{"mainnet", litecoinMainnetWIFKey, false, litecoinMainnetWIF, "ltc1q"},
		{"testnet", litecoinTestnetWIFKey, true, litecoinTestnetWIF, "tltc1q"},
	}
	for _, tc := range cases {
		key, err := NewUTXOKey(mustDecodeHex(t, tc.key), models.ChainLTC, tc.testnet)
		if err != nil || key.WIF != tc.wif {
			t.Fatalf("%s: WIF %v err %v, want %s", tc.name, key, err, tc.wif)
		}
		decoded, err := btcutil.DecodeWIF(tc.wif)
		if err != nil {
			t.Fatal(err)
		}
		want, err := btcutil.NewAddressWitnessPubKeyHash(btcutil.Hash160(decoded.SerializePubKey()), bitcoinchain.BitcoinFamilyParams(models.ChainLTC, tc.testnet))
		if err != nil {
			t.Fatal(err)
		}
		got, err := UTXOP2WPKHAddressOfWIF(tc.wif, models.ChainLTC, tc.testnet)
		if err != nil || got != want.EncodeAddress() || !strings.HasPrefix(got, tc.hrp) {
			t.Fatalf("%s: address %s err %v, want %s", tc.name, got, err, want.EncodeAddress())
		}
		if key.ElectrumImport != "p2wpkh:"+tc.wif || !strings.HasPrefix(key.DescriptorWithChecksum, "wpkh("+tc.wif+")#") {
			t.Fatalf("%s: electrum %q descriptor %q", tc.name, key.ElectrumImport, key.DescriptorWithChecksum)
		}
		if _, err := UTXOP2WPKHAddressOfWIF(tc.wif, models.ChainLTC, !tc.testnet); err == nil {
			t.Fatalf("%s: a litecoin WIF of one network must not verify on the other", tc.name)
		}
	}
	if _, err := UTXOP2WPKHAddressOfWIF(litecoinMainnetWIF, models.ChainBTC, false); err == nil {
		t.Fatal("a litecoin mainnet WIF (0xb0) must not verify as bitcoin mainnet (0x80)")
	}
	tb1, err := UTXOP2WPKHAddressOfWIF(litecoinTestnetWIF, models.ChainBTC, true)
	if err != nil || !strings.HasPrefix(tb1, "tb1") {
		t.Fatalf("testnet WIFs share 0xef; on bitcoin the key controls a tb1 address: %s %v", tb1, err)
	}
	if _, err := UTXOWIF(privateKeyOne(), models.ChainETH, false); err == nil {
		t.Fatal("non Bitcoin-family chains have no WIF")
	}
}

func TestUTXOWIF_PrivateKeyOneControlsTheLitecoinTestnetVector(t *testing.T) {
	wif, err := UTXOWIF(privateKeyOne(), models.ChainLTC, true)
	if err != nil {
		t.Fatal(err)
	}
	address, err := UTXOP2WPKHAddressOfWIF(wif, models.ChainLTC, true)
	if err != nil || address != keyOneLitecoinTestnetAddress {
		t.Fatalf("address %s err %v, want %s", address, err, keyOneLitecoinTestnetAddress)
	}
}

func TestTronKey_PrivateKeyOneIsTheKnownAddress(t *testing.T) {
	key, err := NewTronKey(privateKeyOne())
	if err != nil {
		t.Fatal(err)
	}
	if key.PrivateKeyHex != strings.Repeat("0", 63)+"1" || key.AddressHex != keyOneTronAddressHex {
		t.Fatalf("key %s address hex %s", key.PrivateKeyHex, key.AddressHex)
	}
	address, err := TronAddressOfPrivateKeyHex(key.PrivateKeyHex)
	if err != nil || address != keyOneTronAddress {
		t.Fatalf("address %s err %v, want %s", address, err, keyOneTronAddress)
	}
}

func TestTronKey_MatchesGoEthereumAndRefusesOtherForms(t *testing.T) {
	privateKey := randomSecp256k1Key(t)
	key, err := NewTronKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaKey, err := crypto.HexToECDSA(key.PrivateKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	evmBody := strings.ToLower(strings.TrimPrefix(crypto.PubkeyToAddress(ecdsaKey.PublicKey).Hex(), "0x"))
	if key.AddressHex != "41"+evmBody {
		t.Fatalf("address hex %s, want 41%s", key.AddressHex, evmBody)
	}
	malformed := map[string]string{
		"0x prefix": "0x" + key.PrivateKeyHex, "63 chars": key.PrivateKeyHex[:63], "65 chars": key.PrivateKeyHex + "0",
		"not hex": strings.Repeat("g", 64), "empty": "",
	}
	for name, bad := range malformed {
		if _, err := TronAddressOfPrivateKeyHex(bad); err == nil {
			t.Fatalf("%s: malformed key must be refused", name)
		}
	}
	if _, err := NewTronKey(privateKey[:31]); err == nil {
		t.Fatal("a 31-byte key must be refused")
	}
}

func TestDescriptor_Checksum_MatchesBIP380(t *testing.T) {
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

func TestSolana_Keypair_Base58AndJSONImportToTheSeedAddress(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	publicKey := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	want := base58.Encode(publicKey)

	key, err := NewSolanaKey(SolanaKeyDeps{Seed: seed, KeypairFile: "solana-keygen/index-1.json"})
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
func TestSolana_Scalar_RederivesThePublicKeyButIsNotASeed(t *testing.T) {
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
