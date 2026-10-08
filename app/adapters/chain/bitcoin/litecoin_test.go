package bitcoin

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/bech32"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/pkg/types"
)

// Vectors from litecoin-project/litecoin src/test/data/key_io_valid.json at commit
// ec1b6489a900d09cf5991e220dce089c77a232a2 (address, scriptPubKey or private key).
const (
	ltcMainP2WPKH       = "ltc1qhdhvrwe6rgqns8fz28tee0hphr5x7ulw5exv4w"
	ltcMainP2WPKHScript = "0014bb6ec1bb3a1a01381d2251d79cbee1b8e86f73ee"
	ltcMainP2WSH        = "ltc1qa9dykljtgeayhm8ygx25sc22p0wzgudpe4hw9dyvaz0ye3j5kduq9mf68z"
	ltcMainTaproot      = "ltc1ppu2gv0tujus0f6eggrk7eqmaf0567x6zer4fcuhz4z7ztzq9u9yseqxltc"
	ltcMainWitnessV2    = "ltc1zjwls6j8c4u"
	ltcMainP2PKH        = "LT2KVaAy1ppRuxRgrS5RNU3vBsy7RibPeA"
	ltcMainP2PKHScript  = "76a914558dbca7118cd5894502767c7b2ffc21a22f54db88ac"
	ltcMainP2SH         = "MHrYRxAiMNBTku3eoDHwhA1LQGDjUStZW2"
	ltcMainP2SHScript   = "a9146d328a5b2a20d943a641c8d29b6cc3c2d2df85d387"
	ltcMainWIF          = "T5MZ5z9WqJxzVxYyVPecTJUSDkzDWrUYe1JuSX2AqJ9jKmLJrvTE"
	ltcMainWIFKey       = "44b78d45adc801a65949661d5df1c4a44f532cd422be413a505d776784ddbe25"

	ltcTestP2WPKH       = "tltc1qpftpsvdn6mjp8celrkj0qxqy4jlapl959rlwg9"
	ltcTestP2WPKHScript = "00140a561831b3d6e413e33f1da4f01804acbfd0fcb4"
	ltcTestP2WSH        = "tltc1q9awqm4ah2lmettm3hnhgqtssvz527jxglxxshkt75l0ygdl3vlyqy5739f"
	ltcTestTaproot      = "tltc1pfnh6ljtrdgk4hh3acvu39a742vaqmd2khnd0tp9d0prcnvpq6zgqn0ecgk"
	ltcTestP2PKH        = "mwXn5Vhicdav8Av3KMokx4YNuHoGRKxtWs"
	ltcTestP2PKHScript  = "76a914afa9e672141e283dfbcc1cf28f3793a856d3878488ac"
	ltcTestP2SH         = "QXaUgLHkZcbJb2j5XgMYzY8cF9YM48Y81Z"
	ltcTestP2SHScript   = "a9147860667e60a6545e18e541984094b29c9c46991b87"
	ltcTestWIF          = "cQaeKQwuakynYD9iebyxsKiBKF8RT3G6zoqRNUDybMsAimANRypo"
	ltcTestWIFKey       = "597b8f070b98ee1f997fa3cb976466fa0e931256246b8c7177d2b067eed06ad7"

	// Private key 1 on Litecoin testnet (the hash160 of its key is BIP-173's program).
	ltcKeyOneTestnetAddress = "tltc1qw508d6qejxtdg4y5r3zarvary0c5xw7klfsuq0"
	// Litecoin MWEB stealth addresses are bech32 with their own prefix (mweb_hrp in
	// chainparams.cpp) and are never payable here.
	ltcMWEBHRP = "ltcmweb"
)

// bech32WithPrefix encodes a SegWit v0 program under any human-readable part.
func bech32WithPrefix(t *testing.T, hrp string, program []byte) string {
	t.Helper()
	groups, err := bech32.ConvertBits(program, byteBits, bech32DataGroupBits, true)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := bech32.Encode(hrp, append([]byte{p2wpkhWitnessVersion}, groups...))
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

const (
	ltcFixtureHeight    = uint64(4907128)
	ltcFixtureBlockHash = "052cc3a77b3fbc9c71b31fe8c42780f2553abb287d80524deb0f5e9e0d9ac368"
	ltcFixtureTimestamp = int64(1791126233)
	ltcFixtureTxCount   = 8
	ltcFixtureTransfers = 11
	ltcEsploraPrefix    = "/litecoinspace.org/testnet/api"
)

func mustDecodeLTCHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestLitecoinParams_EncodeLitecoinCoreVectors(t *testing.T) {
	cases := []struct {
		name                        string
		params                      *chaincfg.Params
		p2pkh, p2pkhScript          string
		p2sh, p2shScript            string
		p2wpkh, p2wpkhScript        string
		wif, wifKey                 string
		pubKeyHashID, scriptHashID  byte
		privateKeyID                byte
		hrp                         string
		hdPrivateKeyID, hdPublicKey [4]byte
	}{
		{
			name: "mainnet", params: &ltcMainNetParams,
			p2pkh: ltcMainP2PKH, p2pkhScript: ltcMainP2PKHScript, p2sh: ltcMainP2SH, p2shScript: ltcMainP2SHScript,
			p2wpkh: ltcMainP2WPKH, p2wpkhScript: ltcMainP2WPKHScript, wif: ltcMainWIF, wifKey: ltcMainWIFKey,
			pubKeyHashID: 0x30, scriptHashID: 0x32, privateKeyID: 0xb0, hrp: "ltc",
			hdPrivateKeyID: [4]byte{0x04, 0x88, 0xad, 0xe4}, hdPublicKey: [4]byte{0x04, 0x88, 0xb2, 0x1e},
		},
		{
			name: "testnet", params: &ltcTestNetParams,
			p2pkh: ltcTestP2PKH, p2pkhScript: ltcTestP2PKHScript, p2sh: ltcTestP2SH, p2shScript: ltcTestP2SHScript,
			p2wpkh: ltcTestP2WPKH, p2wpkhScript: ltcTestP2WPKHScript, wif: ltcTestWIF, wifKey: ltcTestWIFKey,
			pubKeyHashID: 0x6f, scriptHashID: 0x3a, privateKeyID: 0xef, hrp: "tltc",
			hdPrivateKeyID: [4]byte{0x04, 0x35, 0x83, 0x94}, hdPublicKey: [4]byte{0x04, 0x35, 0x87, 0xcf},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.params
			if p.PubKeyHashAddrID != tc.pubKeyHashID || p.ScriptHashAddrID != tc.scriptHashID || p.PrivateKeyID != tc.privateKeyID ||
				p.Bech32HRPSegwit != tc.hrp || p.HDPrivateKeyID != tc.hdPrivateKeyID || p.HDPublicKeyID != tc.hdPublicKey {
				t.Fatalf("params %+v", p)
			}

			p2pkh, err := btcutil.NewAddressPubKeyHash(mustDecodeLTCHex(t, tc.p2pkhScript)[3:23], p)
			if err != nil || p2pkh.EncodeAddress() != tc.p2pkh {
				t.Fatalf("P2PKH %v %v, want %s", p2pkh, err, tc.p2pkh)
			}
			p2sh, err := btcutil.NewAddressScriptHashFromHash(mustDecodeLTCHex(t, tc.p2shScript)[2:22], p)
			if err != nil || p2sh.EncodeAddress() != tc.p2sh {
				t.Fatalf("P2SH %v %v, want %s", p2sh, err, tc.p2sh)
			}
			p2wpkh, err := btcutil.NewAddressWitnessPubKeyHash(mustDecodeLTCHex(t, tc.p2wpkhScript)[2:], p)
			if err != nil || p2wpkh.EncodeAddress() != tc.p2wpkh {
				t.Fatalf("P2WPKH %v %v, want %s", p2wpkh, err, tc.p2wpkh)
			}
			decoded, err := btcutil.DecodeAddress(tc.p2pkh, p)
			if err != nil || !decoded.IsForNet(p) {
				t.Fatalf("base58 %s does not decode on %s: %v", tc.p2pkh, p.Name, err)
			}

			key, _ := btcec.PrivKeyFromBytes(mustDecodeLTCHex(t, tc.wifKey))
			wif, err := btcutil.NewWIF(key, p, true)
			if err != nil || wif.String() != tc.wif {
				t.Fatalf("WIF %v %v, want %s", wif, err, tc.wif)
			}
			parsed, err := btcutil.DecodeWIF(tc.wif)
			if err != nil || !parsed.IsForNet(p) || !parsed.CompressPubKey || !bytes.Equal(parsed.PrivKey.Serialize(), mustDecodeLTCHex(t, tc.wifKey)) {
				t.Fatalf("vector WIF does not decode to its key on %s: %v", p.Name, err)
			}
		})
	}
}

// btcutil.DecodeAddress only tries bech32 for prefixes registered with chaincfg, so
// it cannot read ltc1/tltc1 with unregistered parameters; witnessProgram can.
func TestLitecoinBech32_DecodedByTheAdapterWithoutGlobalRegistration(t *testing.T) {
	for _, prefix := range []string{"ltc1", "tltc1"} {
		if chaincfg.IsBech32SegwitPrefix(prefix) {
			t.Fatalf("%s must not be registered with chaincfg", prefix)
		}
	}
	if _, err := btcutil.DecodeAddress(ltcMainP2WPKH, &ltcMainNetParams); err == nil {
		t.Fatal("btcutil decodes ltc1 without registration; witnessProgram may be replaced by it")
	}
	for address, script := range map[string]string{ltcMainP2WPKH: ltcMainP2WPKHScript, ltcTestP2WPKH: ltcTestP2WPKHScript} {
		params := &ltcMainNetParams
		if strings.HasPrefix(address, addressing.LtcHRPTestnet) {
			params = &ltcTestNetParams
		}
		program, err := witnessProgram(address, params)
		if err != nil || !bytes.Equal(p2wpkhPkScript(program), mustDecodeLTCHex(t, script)) {
			t.Fatalf("%s: script %x err %v, want %s", address, p2wpkhPkScript(program), err, script)
		}
	}
}

func TestLitecoinValidateAddress_AcceptsOnlyP2WPKHOfItsNetwork(t *testing.T) {
	mainnet := NewBitcoinLive(BitcoinConfig{ChainIDStr: models.ChainLTC, NativeSymbol: models.NativeLTC})
	testnet := NewBitcoinLive(BitcoinConfig{ChainIDStr: models.ChainLTC, NativeSymbol: models.NativeLTC, IsTestnet: true})
	bitcoin := NewBitcoinLive(BitcoinConfig{ChainIDStr: models.ChainBTC})
	mweb := bech32WithPrefix(t, ltcMWEBHRP, mustWitnessProgram(t, ltcMainP2WPKH, &ltcMainNetParams))

	cases := []struct {
		name    string
		adapter *BitcoinLive
		address string
		want    bool
	}{
		{"mainnet P2WPKH", mainnet, ltcMainP2WPKH, true},
		{"mainnet P2WPKH uppercase", mainnet, strings.ToUpper(ltcMainP2WPKH), true},
		{"mainnet P2WPKH, other vector", mainnet, "ltc1qxjkwr09apz3w5hsr33uyq9sdfx8tn26g39g00g", true},
		{"mainnet P2WSH", mainnet, ltcMainP2WSH, false},
		{"mainnet taproot", mainnet, ltcMainTaproot, false},
		{"mainnet witness v2", mainnet, ltcMainWitnessV2, false},
		{"mainnet P2PKH", mainnet, ltcMainP2PKH, false},
		{"mainnet P2SH", mainnet, ltcMainP2SH, false},
		{"mainnet MWEB", mainnet, mweb, false},
		{"testnet address on mainnet", mainnet, ltcTestP2WPKH, false},
		{"bitcoin address on litecoin", mainnet, "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4", false},
		{"mixed case", mainnet, "ltc1qHdhvrwe6rgqns8fz28tee0hphr5x7ulw5exv4w", false},
		{"empty", mainnet, "", false},
		{"testnet P2WPKH", testnet, ltcTestP2WPKH, true},
		{"testnet key 1", testnet, ltcKeyOneTestnetAddress, true},
		{"testnet P2WSH", testnet, ltcTestP2WSH, false},
		{"testnet taproot", testnet, ltcTestTaproot, false},
		{"testnet P2PKH", testnet, ltcTestP2PKH, false},
		{"testnet P2SH", testnet, ltcTestP2SH, false},
		{"mainnet address on testnet", testnet, ltcMainP2WPKH, false},
		{"bitcoin testnet address on litecoin testnet", testnet, "tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx", false},
		{"litecoin address on bitcoin", bitcoin, ltcMainP2WPKH, false},
	}
	for _, tc := range cases {
		if got := tc.adapter.ValidateAddress(tc.address); got != tc.want {
			t.Errorf("%s: ValidateAddress(%s) = %t, want %t", tc.name, tc.address, got, tc.want)
		}
	}
}

func TestLitecoinPrivateKeyOneDerivesTheVectorAddress(t *testing.T) {
	one := make([]byte, 32)
	one[31] = 1
	key, _ := btcec.PrivKeyFromBytes(one)
	address, err := addressing.DeriveAddressOnNetwork(models.ChainLTC, true, key.PubKey().SerializeCompressed())
	if err != nil || address != ltcKeyOneTestnetAddress {
		t.Fatalf("address %s err %v, want %s", address, err, ltcKeyOneTestnetAddress)
	}
}

func TestBitcoinNetworkOf_PicksTheNetworkFromTheChainRecord(t *testing.T) {
	cases := []struct {
		cfg      BitcoinConfig
		name     string
		hrp      string
		feeAsset string
		testnet  bool
		dust     int64
	}{
		{BitcoinConfig{ChainIDStr: models.ChainBTC}, models.NetworkBitcoinMainnet, "bc", "BTC", false, 546},
		{BitcoinConfig{ChainIDStr: models.ChainBTC, IsTestnet: true}, models.NetworkBitcoinTestnet, "tb", "TBTC", true, 546},
		{BitcoinConfig{ChainIDStr: models.ChainBTC, IsTestnet: true, RPCURL: "https://mempool.space/testnet4/api"}, models.NetworkBitcoinTestnet4, "tb", "TBTC", true, 546},
		{BitcoinConfig{ChainIDStr: models.ChainTBTC}, models.NetworkBitcoinTestnet, "tb", "TBTC", true, 546},
		{BitcoinConfig{ChainIDStr: models.ChainLTC}, models.NetworkLitecoinMainnet, "ltc", "LTC", false, 5_460},
		{BitcoinConfig{ChainIDStr: models.ChainLTC, IsTestnet: true}, models.NetworkLitecoinTestnet, "tltc", "TLTC", true, 5_460},
		{BitcoinConfig{ChainIDStr: models.ChainTLTC}, models.NetworkLitecoinTestnet, "tltc", "TLTC", true, 5_460},
	}
	for _, tc := range cases {
		live := NewBitcoinLive(tc.cfg)
		network := live.network
		if network.name != tc.name || network.params.Bech32HRPSegwit != tc.hrp || network.feeAsset != tc.feeAsset ||
			live.IsTestnet() != tc.testnet || live.MinimumTransferAmount().Int64() != tc.dust {
			t.Errorf("%+v: network %s/%s/%s testnet %t dust %s", tc.cfg, network.name, network.params.Bech32HRPSegwit, network.feeAsset, live.IsTestnet(), live.MinimumTransferAmount())
		}
	}
	if NewBitcoinLive(BitcoinConfig{ChainIDStr: models.ChainLTC, RPCURL: "https://litecoinspace.org/testnet/api"}).restAPI != true {
		t.Fatal("litecoinspace must be spoken to over the Esplora REST API")
	}
}

func TestBitcoinFamilyParams_ReturnsACopyPerChain(t *testing.T) {
	params := BitcoinFamilyParams(models.ChainLTC, false)
	if params == nil || params.PrivateKeyID != ltcMainNetPrivateKeyID {
		t.Fatalf("ltc params %+v", params)
	}
	params.PrivateKeyID = 0
	if ltcMainNetParams.PrivateKeyID != ltcMainNetPrivateKeyID {
		t.Fatal("callers must not be able to change the adapter's parameters")
	}
	if got := BitcoinFamilyParams(models.ChainTLTC, false); got == nil || got.Bech32HRPSegwit != addressing.LtcHRPTestnet {
		t.Fatalf("tltc params %+v", got)
	}
	if got := BitcoinFamilyParams(models.ChainBTC, true); got == nil || got.PrivateKeyID != chaincfg.TestNet3Params.PrivateKeyID {
		t.Fatalf("btc testnet params %+v", got)
	}
	if BitcoinFamilyParams(models.ChainETH, false) != nil {
		t.Fatal("non Bitcoin-family chains have no UTXO parameters")
	}
}

// ---------------------------------------------------------------------------
// Fee rate
// ---------------------------------------------------------------------------

// liveLitecoinTestnetRecommendedFees is a real litecoinspace testnet answer.
const liveLitecoinTestnetRecommendedFees = `{"fastestFee":1,"halfHourFee":1,"hourFee":1,"economyFee":1,"minimumFee":1}`

func TestParseMempoolRecommendedFees(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    int64
		wantErr bool
	}{
		{"live litecoinspace testnet answer", liveLitecoinTestnetRecommendedFees, 1000, false},
		{"hourFee is the third-block target", `{"fastestFee":30,"halfHourFee":20,"hourFee":12.3,"economyFee":2,"minimumFee":1}`, 12300, false},
		{"halfHourFee stands in for a missing hourFee", `{"fastestFee":30,"halfHourFee":7.25,"minimumFee":1}`, 7250, false},
		{"floored at minimumFee", `{"hourFee":2,"minimumFee":3.5}`, 3500, false},
		{"sub-relay rate lifted to 1 sat/vB", `{"hourFee":0.25}`, btcMinRelayMilliSatPerVByte, false},
		{"milli-sat rounding is exact", `{"hourFee":1.0005}`, 1001, false},
		{"neither target", `{"fastestFee":30,"economyFee":1}`, 0, true},
		{"zero rate", `{"hourFee":0}`, 0, true},
		{"negative rate", `{"hourFee":-1}`, 0, true},
		{"absurd rate", `{"hourFee":1e9}`, 0, true},
		{"string rate", `{"hourFee":"12"}`, 0, true},
		{"null rate", `{"hourFee":null}`, 0, true},
		{"bad minimumFee", `{"hourFee":2,"minimumFee":"x"}`, 0, true},
		{"esplora format", `{"3":12.3,"6":5}`, 0, true},
		{"not an object", `[1,2]`, 0, true},
		{"empty", ``, 0, true},
	}
	for _, tc := range cases {
		got, err := parseMempoolRecommendedFees([]byte(tc.body))
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("%s: rate %d err %v, want %d (error %t)", tc.name, got, err, tc.want, tc.wantErr)
		}
	}
}

// litecoinAdapter is a Litecoin testnet adapter against a fake whose path names
// litecoinspace, so the REST client is picked like in production.
func litecoinAdapter(esplora *fakeEsplora) *BitcoinLive {
	live := NewBitcoinLive(BitcoinConfig{
		ChainIDStr: models.ChainLTC, ChainName: "Litecoin", NativeSymbol: models.NativeLTC,
		NativeDecimal: btcDecimals, RPCURL: esplora.srv.URL + ltcEsploraPrefix, IsTestnet: true, Confirmations: 6,
	})
	live.esploraRetry.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return live
}

func TestLitecoinFeeRateComesFromRecommendedFees(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(ltcEsploraPrefix+mempoolRecommendedFeesPath, `{"fastestFee":3,"halfHourFee":2.5,"hourFee":1.7,"economyFee":1,"minimumFee":1}`)
	live := litecoinAdapter(esplora)
	if !live.restAPI {
		t.Fatal("adapter must use the Esplora REST client")
	}

	policy := live.feePolicy(context.Background())
	estimate, err := live.EstimateFee(context.Background(), types.TransferRequest{})

	if policy.milliSatPerVByte != 1700 || policy.dust() != ltcDustSats {
		t.Fatalf("policy %+v", policy)
	}
	wantFee := ceilDiv(p2wpkhVSize(btcTypicalInputs, btcOutputsPaymentWithChange)*1700, milliSatsPerSat)
	if err != nil || estimate.FeeAsset != ltcFeeAssetTestnet || estimate.Fee != fmtUnits(big.NewInt(wantFee), btcDecimals) {
		t.Fatalf("estimate %+v err %v, want %d sats in TLTC", estimate, err, wantFee)
	}
	if hits := esplora.hitCount(ltcEsploraPrefix + esploraFeeEstimatesPath); hits != 0 {
		t.Fatalf("/fee-estimates fetched %d times; litecoin asks /v1/fees/recommended first", hits)
	}
}

func TestLitecoinFeeRateFallsBackToTheFlatFeeWhenNoSourceAnswers(t *testing.T) {
	esplora := newFakeEsplora(t)
	live := litecoinAdapter(esplora)

	policy := live.feePolicy(context.Background())

	if policy.milliSatPerVByte != 0 || policy.flatFee != flatTestFee {
		t.Fatalf("policy %+v, want the flat fallback", policy)
	}
	if esplora.hitCount(ltcEsploraPrefix+mempoolRecommendedFeesPath) != 1 || esplora.hitCount(ltcEsploraPrefix+esploraFeeEstimatesPath) != 1 {
		t.Fatal("both sources must be tried once when each answers 404")
	}
}

func TestBitcoinFeeRateTriesRecommendedFeesOnlyWhenFeeEstimatesIsNotServed(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(esploraTestPrefix+mempoolRecommendedFeesPath, `{"halfHourFee":9,"hourFee":4,"minimumFee":1}`)
	if policy := feeTestAdapter(esplora).feePolicy(context.Background()); policy.milliSatPerVByte != 4000 {
		t.Fatalf("404 on /fee-estimates must fall back to hourFee: %+v", policy)
	}

	failing := newFakeEsplora(t)
	failing.on(esploraTestPrefix+esploraFeeEstimatesPath, esploraAnswer{http.StatusInternalServerError, "boom"})
	failing.ok(esploraTestPrefix+mempoolRecommendedFeesPath, `{"hourFee":4}`)
	if policy := feeTestAdapter(failing).feePolicy(context.Background()); policy.milliSatPerVByte != 0 {
		t.Fatalf("a failing /fee-estimates must give the flat fee, not another source: %+v", policy)
	}
	if failing.hitCount(esploraTestPrefix+mempoolRecommendedFeesPath) != 0 {
		t.Fatal("only a 404 passes to the next source")
	}
}

// ---------------------------------------------------------------------------
// Build, sign, verify
// ---------------------------------------------------------------------------

type litecoinWallet struct {
	key     *btcec.PrivateKey
	address string
}

func newLitecoinTestnetWallet(t *testing.T) litecoinWallet {
	t.Helper()
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	address, err := addressing.DeriveBtcAddress(addressing.LtcHRPTestnet, key.PubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	return litecoinWallet{key: key, address: address}
}

func fundLitecoinWallet(esplora *fakeEsplora, address string, values ...int64) {
	inputs := make([]btcInput, 0, len(values))
	for i, value := range values {
		inputs = append(inputs, btcInput{TxID: fmt.Sprintf("%064x", i+0xa1), Vout: uint32(i), Value: value, Address: address})
	}
	esplora.ok(ltcEsploraPrefix+"/address/"+address+"/utxo", utxoJSON(inputs, true))
	esplora.ok(ltcEsploraPrefix+mempoolRecommendedFeesPath, `{"hourFee":1,"minimumFee":1}`)
}

func TestLitecoinBuildSignVerifyP2WPKH(t *testing.T) {
	esplora := newFakeEsplora(t)
	wallet := newLitecoinTestnetWallet(t)
	fundLitecoinWallet(esplora, wallet.address, 1_000_000)
	live := litecoinAdapter(esplora)

	unsigned, err := live.BuildTransfer(context.Background(), types.TransferRequest{From: wallet.address, To: ltcTestP2WPKH, Amount: big.NewInt(100_000)})
	if err != nil {
		t.Fatal(err)
	}
	signed := signBitcoinP2WPKHForTest(t, unsigned, wallet.key.Serialize())
	if err := live.VerifySignedTransaction(unsigned, signed, wallet.address); err != nil {
		t.Fatalf("litecoin signature must verify: %v", err)
	}

	var msg wire.MsgTx
	if err := msg.Deserialize(bytes.NewReader(signed.RawBytes)); err != nil {
		t.Fatal(err)
	}
	wantFee := p2wpkhVSize(1, 2)
	if len(msg.TxOut) != 2 || !bytes.Equal(msg.TxOut[0].PkScript, mustDecodeLTCHex(t, ltcTestP2WPKHScript)) || msg.TxOut[0].Value != 100_000 ||
		msg.TxOut[1].Value != 1_000_000-100_000-wantFee || unsigned.Metadata[btcMetadataNetwork] != models.NetworkLitecoinTestnet {
		t.Fatalf("outputs %+v metadata %+v", msg.TxOut, unsigned.Metadata)
	}
	if !bytes.Equal(btcutil.Hash160(msg.TxIn[0].Witness[1]), mustWitnessProgram(t, wallet.address, &ltcTestNetParams)) {
		t.Fatal("witness key must own the tltc1 input")
	}
}

func mustWitnessProgram(t *testing.T, address string, params *chaincfg.Params) []byte {
	t.Helper()
	program, err := witnessProgram(address, params)
	if err != nil {
		t.Fatal(err)
	}
	return program
}

func TestLitecoinSignAndVerifyRefuseAnotherNetwork(t *testing.T) {
	esplora := newFakeEsplora(t)
	wallet := newLitecoinTestnetWallet(t)
	fundLitecoinWallet(esplora, wallet.address, 500_000)
	testnet := litecoinAdapter(esplora)
	unsigned, err := testnet.BuildTransfer(context.Background(), types.TransferRequest{From: wallet.address, To: ltcKeyOneTestnetAddress, Amount: big.NewInt(50_000)})
	if err != nil {
		t.Fatal(err)
	}
	signed := signBitcoinP2WPKHForTest(t, unsigned, wallet.key.Serialize())

	mainnet := NewBitcoinLive(BitcoinConfig{ChainIDStr: models.ChainLTC, NativeSymbol: models.NativeLTC})
	bitcoinTestnet := NewBitcoinLive(BitcoinConfig{ChainIDStr: models.ChainBTC, IsTestnet: true})
	for name, other := range map[string]*BitcoinLive{"litecoin mainnet": mainnet, "bitcoin testnet": bitcoinTestnet} {
		if _, err := other.BitcoinP2WPKHDigests(unsigned); err == nil {
			t.Errorf("%s adapter produced sighashes for a litecoin testnet transaction", name)
		}
		if _, err := other.AssembleBitcoinP2WPKH(unsigned, [][]byte{{0x01}}, [][]byte{{0x02}}); err == nil {
			t.Errorf("%s adapter assembled a litecoin testnet transaction", name)
		}
		if err := other.VerifySignedTransaction(unsigned, signed, wallet.address); err == nil {
			t.Errorf("%s adapter verified a litecoin testnet transaction", name)
		}
	}
	if err := verifySignedP2WPKH(unsigned, signed, wallet.address, &chaincfg.TestNet3Params); err == nil {
		t.Fatal("tltc1 inputs must not decode with Bitcoin testnet parameters")
	}

	bitcoinOutput := cloneBitcoinUnsigned(unsigned)
	bitcoinOutput.Metadata["outputs"] = []btcOutput{{Address: "tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx", Value: 50_000}}
	if _, err := testnet.BitcoinP2WPKHDigests(bitcoinOutput); err == nil {
		t.Fatal("a litecoin transaction paying a tb1 address must not be signed")
	}
}

func cloneBitcoinUnsigned(unsigned *types.UnsignedTx) *types.UnsignedTx {
	metadata := make(map[string]interface{}, len(unsigned.Metadata))
	for key, value := range unsigned.Metadata {
		metadata[key] = value
	}
	clone := *unsigned
	clone.Metadata = metadata
	return &clone
}

func TestLitecoinDustLimitIsLitecoinCores(t *testing.T) {
	esplora := newFakeEsplora(t)
	wallet := newLitecoinTestnetWallet(t)
	fundLitecoinWallet(esplora, wallet.address, 100_000)
	live := litecoinAdapter(esplora)

	if _, err := live.BuildTransfer(context.Background(), types.TransferRequest{From: wallet.address, To: ltcTestP2WPKH, Amount: big.NewInt(ltcDustSats - 1)}); err == nil {
		t.Fatal("an amount below the litecoin dust limit must be refused")
	}
	withChange := p2wpkhVSize(1, 2)
	amount := 100_000 - withChange - (ltcDustSats - 1)
	unsigned, err := live.BuildTransfer(context.Background(), types.TransferRequest{From: wallet.address, To: ltcTestP2WPKH, Amount: big.NewInt(amount)})
	if err != nil {
		t.Fatal(err)
	}
	outputs, _ := outputsFrom(unsigned)
	if len(outputs) != 1 || unsigned.Metadata["fee"] != 100_000-amount {
		t.Fatalf("change below %d sats must go to the fee: outputs %+v fee %v", ltcDustSats, outputs, unsigned.Metadata["fee"])
	}
}

// ---------------------------------------------------------------------------
// Block scanning
// ---------------------------------------------------------------------------

func readLitecoinFixture(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile("testdata/litecoin/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// The fixture is litecoinspace testnet block 4907128 as served on 2026-10-04: seven
// P2WPKH transactions and the MWEB integration transaction (HogEx), whose witness
// v8 output scanners see as an unknown tltc1g... address.
func TestLitecoinScanBlockREST_Fixture(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(fmt.Sprintf("%s/block-height/%d", ltcEsploraPrefix, ltcFixtureHeight), ltcFixtureBlockHash)
	esplora.ok(ltcEsploraPrefix+"/block/"+ltcFixtureBlockHash, readLitecoinFixture(t, "testnet_block_4907128.json"))
	esplora.ok(ltcEsploraPrefix+"/block/"+ltcFixtureBlockHash+"/txs/0", readLitecoinFixture(t, "testnet_block_4907128_txs_0.json"))

	transfers, err := litecoinAdapter(esplora).ScanBlock(context.Background(), ltcFixtureHeight)

	if err != nil {
		t.Fatal(err)
	}
	if len(transfers) != ltcFixtureTransfers {
		t.Fatalf("transfers %d, want %d (every output with an address and value)", len(transfers), ltcFixtureTransfers)
	}
	txs := map[string]bool{}
	var payment *types.DetectedTransfer
	for i := range transfers {
		transfer := transfers[i]
		txs[transfer.TxHash] = true
		if transfer.Asset != models.NativeLTC || transfer.BlockHash != ltcFixtureBlockHash || transfer.BlockNumber != ltcFixtureHeight || transfer.Timestamp.Unix() != ltcFixtureTimestamp {
			t.Fatalf("transfer %+v", transfer)
		}
		if !strings.HasPrefix(transfer.To, addressing.LtcHRPTestnet+"1") {
			t.Fatalf("transfer to %s is not a litecoin testnet address", transfer.To)
		}
		if transfer.TxHash == "68d5883757c56addcbde4ae0fd2817d3145926855aa1556355a3a067baef3aac" && transfer.To == "tltc1qkzyvmrnwsc4t3z87xh8q4z79h7km5zfrcc4q8v" {
			payment = &transfer
		}
	}
	if len(txs) != ltcFixtureTxCount || payment == nil || payment.Amount.Int64() != 200_000 {
		t.Fatalf("txs %d payment %+v", len(txs), payment)
	}
}

// TestLitecoinLive_ReadsTheTestnetTip reads (GET only) the litecoinspace testnet:
// LTC_LIVE_TEST=1 go test ./app/services/chain -run TestLitecoinLive -v
func TestLitecoinLive_ReadsTheTestnetTip(t *testing.T) {
	if os.Getenv("LTC_LIVE_TEST") != "1" {
		t.Skip("set LTC_LIVE_TEST=1 to read the public litecoinspace testnet API")
	}
	const liveURL = "https://litecoinspace.org/testnet/api"
	const scanDepth = 6
	live := NewBitcoinLive(BitcoinConfig{ChainIDStr: models.ChainLTC, ChainName: "Litecoin", NativeSymbol: models.NativeLTC, RPCURL: liveURL, IsTestnet: true, Confirmations: scanDepth})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	tip, err := live.GetLatestBlock(ctx)
	if err != nil || tip <= scanDepth {
		t.Fatalf("tip %d err %v", tip, err)
	}
	height := tip - scanDepth
	transfers, err := live.ScanBlock(ctx, height)
	if err != nil || len(transfers) == 0 {
		t.Fatalf("block %d: %d transfers err %v", height, len(transfers), err)
	}
	payable := 0
	for _, transfer := range transfers {
		if transfer.BlockNumber != height || transfer.Asset != models.NativeLTC || transfer.Amount.Sign() <= 0 {
			t.Fatalf("transfer %+v", transfer)
		}
		if live.ValidateAddress(transfer.To) {
			payable++
		}
	}
	rate, err := live.fetchFeeRate(ctx)
	if err != nil || rate < btcMinRelayMilliSatPerVByte {
		t.Fatalf("fee rate %d err %v", rate, err)
	}
	t.Logf("tip %d; block %d (%s): %d transfers, %d to P2WPKH addresses; fee rate %d milli-sat/vB",
		tip, height, transfers[0].BlockHash, len(transfers), payable, rate)
}
