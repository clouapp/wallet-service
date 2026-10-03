package addressing

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

// BIP-173 test vector: this key's P2WPKH is bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4.
const bip173PubKey = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"

func mustPubKey(t *testing.T) []byte {
	t.Helper()
	pub, err := hex.DecodeString(bip173PubKey)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func TestBitcoinRecordOnTestnetDerivesTb1UnderTheMainnetChainID(t *testing.T) {
	t.Parallel()
	pub := mustPubKey(t)

	mainnet, err := DeriveAddressOnNetwork(models.ChainBTC, false, pub)
	if err != nil || mainnet != "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4" {
		t.Fatalf("mainnet = %q, %v", mainnet, err)
	}
	testnet, err := DeriveAddressOnNetwork(models.ChainBTC, true, pub)
	if err != nil || testnet != "tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx" {
		t.Fatalf("testnet = %q, %v", testnet, err)
	}
	tbtc, err := DeriveAddress(models.ChainTBTC, pub)
	if err != nil || tbtc != testnet {
		t.Fatalf("tbtc = %q, %v; want the same key as %q", tbtc, err, testnet)
	}
}

func TestNetworkDoesNotChangeEVMAddresses(t *testing.T) {
	t.Parallel()
	pub := mustPubKey(t)

	onMainnet, err := DeriveAddressOnNetwork(models.ChainETH, false, pub)
	if err != nil {
		t.Fatal(err)
	}
	onSepolia, err := DeriveAddressOnNetwork(models.ChainETH, true, pub)
	if err != nil {
		t.Fatal(err)
	}
	if onMainnet != onSepolia || !strings.HasPrefix(onMainnet, "0x") {
		t.Fatalf("eth address changed with the network: %q vs %q", onMainnet, onSepolia)
	}
}

func TestBaseArbitrumAndBSCShareTheEthereumAddressOfTheKey(t *testing.T) {
	t.Parallel()
	pub := mustPubKey(t)

	eth, err := DeriveAddress(models.ChainETH, pub)
	if err != nil {
		t.Fatal(err)
	}
	for _, chainID := range []string{models.ChainBase, models.ChainTBase, models.ChainArbitrum, models.ChainTArbitrum, models.ChainBSC, models.ChainTBSC} {
		got, err := DeriveAddressOnNetwork(chainID, true, pub)
		if err != nil || got != eth {
			t.Errorf("%s: %q, %v; want the eth address %q", chainID, got, err, eth)
		}
	}
}

func TestBtcHRPAndUnsupportedChains(t *testing.T) {
	t.Parallel()

	if BtcHRP(true) != BtcHRPTestnet || BtcHRP(false) != BtcHRPMainnet {
		t.Fatal("BtcHRP picked the wrong prefix")
	}
	if _, err := DeriveAddressOnNetwork("doge", true, mustPubKey(t)); err == nil {
		t.Fatal("unsupported chain accepted")
	}
	if _, err := DeriveAddressOnNetwork(models.ChainBTC, true, []byte{1, 2, 3}); err == nil {
		t.Fatal("invalid public key accepted")
	}
}
