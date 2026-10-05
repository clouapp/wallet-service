package keyexport

import (
	"encoding/hex"
	"testing"
)

func TestNewBitcoinKeyUsesDeps(t *testing.T) {
	privateKey := randomSecp256k1Key(t)

	main, err := NewBitcoinKey(BitcoinKeyDeps{PrivateKey: privateKey})
	if err != nil {
		t.Fatal(err)
	}
	testnet, err := NewBitcoinKey(BitcoinKeyDeps{PrivateKey: privateKey, Testnet: true})
	if err != nil {
		t.Fatal(err)
	}

	mainWIF, err := BitcoinWIF(privateKey, false)
	if err != nil {
		t.Fatal(err)
	}
	testnetWIF, err := BitcoinWIF(privateKey, true)
	if err != nil {
		t.Fatal(err)
	}
	mainChecksum, err := DescriptorChecksum("wpkh(" + mainWIF + ")")
	if err != nil {
		t.Fatal(err)
	}
	if main.WIF != mainWIF || testnet.WIF != testnetWIF || main.WIF == testnet.WIF {
		t.Fatal("network flag was not applied")
	}
	if main.PrivateKeyHex != hex.EncodeToString(privateKey) || testnet.PrivateKeyHex != main.PrivateKeyHex {
		t.Fatal("private key hex was not taken from deps")
	}
	if main.ElectrumImport != electrumP2WPKHPrefix+main.WIF || main.Descriptor != "wpkh("+main.WIF+")" {
		t.Fatal("electrum line or descriptor was not built from the wif")
	}
	if main.DescriptorWithChecksum != main.Descriptor+"#"+mainChecksum {
		t.Fatal("descriptor checksum was not applied")
	}
	if _, err := NewBitcoinKey(BitcoinKeyDeps{}); err == nil {
		t.Fatal("a missing private key was accepted")
	}
	if _, err := NewBitcoinKey(BitcoinKeyDeps{PrivateKey: privateKey[:len(privateKey)-1], Testnet: true}); err == nil {
		t.Fatal("a short private key was accepted")
	}
}
