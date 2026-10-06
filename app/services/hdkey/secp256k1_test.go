package hdkey

import (
	"bytes"
	"crypto/rand"
	"math/big"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
)

func randomParent(t *testing.T) (privateKey, publicKey, chainCode []byte) {
	t.Helper()
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	chainCode = make([]byte, ChainCodeSize)
	if _, err := rand.Read(chainCode); err != nil {
		t.Fatal(err)
	}
	return key.Serialize(), key.PubKey().SerializeCompressed(), chainCode
}

// The public derivation used for addresses and the private tweak used for signing
// must both agree with btcutil's independent BIP-32 implementation.
func TestDerive_Secp2561Child_MatchesBIP32PrivateDerivation(t *testing.T) {
	parentPrivate, parentPublic, chainCode := randomParent(t)
	extended := hdkeychain.NewExtendedKey(chaincfg.MainNetParams.HDPrivateKeyID[:], parentPrivate, chainCode, []byte{0, 0, 0, 0}, 0, 0, true)

	for _, index := range []uint32{0, 1, 25, 69, hdkeychain.HardenedKeyStart - 1} {
		child, err := DeriveSecp256k1Child(parentPublic, chainCode, index)
		if err != nil {
			t.Fatal(err)
		}
		reference, err := extended.Derive(index)
		if err != nil {
			t.Fatal(err)
		}
		referencePublic, err := reference.ECPubKey()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(child.PublicKey, referencePublic.SerializeCompressed()) {
			t.Fatalf("index %d: public child differs from BIP-32", index)
		}
		childPrivate, err := ChildPrivateKey(parentPrivate, child.Tweak)
		if err != nil {
			t.Fatal(err)
		}
		referencePrivate, err := reference.ECPrivKey()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(childPrivate, referencePrivate.Serialize()) {
			t.Fatalf("index %d: private child differs from BIP-32", index)
		}
		derivedPublic, err := PublicKeyOf(childPrivate)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(derivedPublic, child.PublicKey) {
			t.Fatalf("index %d: tweaked private key does not own the child public key", index)
		}
	}
}

func TestDerive_Secp2561Child_RejectsInvalidInput(t *testing.T) {
	_, parentPublic, chainCode := randomParent(t)
	cases := map[string]func() error{
		"short public key": func() error { _, err := DeriveSecp256k1Child(parentPublic[:32], chainCode, 1); return err },
		"short chain code": func() error { _, err := DeriveSecp256k1Child(parentPublic, chainCode[:31], 1); return err },
		"hardened index": func() error {
			_, err := DeriveSecp256k1Child(parentPublic, chainCode, hdkeychain.HardenedKeyStart)
			return err
		},
		"point off the curve": func() error {
			offCurve := append([]byte{0x02}, bytes.Repeat([]byte{0xff}, 32)...)
			_, err := DeriveSecp256k1Child(offCurve, chainCode, 1)
			return err
		},
	}
	for name, run := range cases {
		if run() == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestChild_PrivateKey_RejectsOutOfRangeInput(t *testing.T) {
	parentPrivate, _, _ := randomParent(t)
	n := btcec.S256().N
	order := make([]byte, PrivateKeySize)
	n.FillBytes(order)
	cases := map[string]func() error{
		"nil tweak":      func() error { _, err := ChildPrivateKey(parentPrivate, nil); return err },
		"zero tweak":     func() error { _, err := ChildPrivateKey(parentPrivate, big.NewInt(0)); return err },
		"tweak equals n": func() error { _, err := ChildPrivateKey(parentPrivate, new(big.Int).Set(n)); return err },
		"short parent":   func() error { _, err := ChildPrivateKey(parentPrivate[:31], big.NewInt(1)); return err },
		"zero parent":    func() error { _, err := ChildPrivateKey(make([]byte, PrivateKeySize), big.NewInt(1)); return err },
		"parent equals n": func() error {
			_, err := ChildPrivateKey(order, big.NewInt(1))
			return err
		},
		"child is zero": func() error {
			parent := new(big.Int).Sub(n, big.NewInt(1))
			parentBytes := make([]byte, PrivateKeySize)
			parent.FillBytes(parentBytes)
			_, err := ChildPrivateKey(parentBytes, big.NewInt(1))
			return err
		},
	}
	for name, run := range cases {
		if run() == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestChild_PrivateKey_WrapsModuloTheOrder(t *testing.T) {
	n := btcec.S256().N
	parent := new(big.Int).Sub(n, big.NewInt(1))
	parentBytes := make([]byte, PrivateKeySize)
	parent.FillBytes(parentBytes)

	child, err := ChildPrivateKey(parentBytes, big.NewInt(5))
	if err != nil {
		t.Fatal(err)
	}
	if new(big.Int).SetBytes(child).Cmp(big.NewInt(4)) != 0 {
		t.Fatalf("(n-1)+5 mod n should be 4, got %x", child)
	}
}

func TestPublic_KeyOf_RejectsOutOfRangeKeys(t *testing.T) {
	order := make([]byte, PrivateKeySize)
	btcec.S256().N.FillBytes(order)
	for name, key := range map[string][]byte{"zero": make([]byte, PrivateKeySize), "order": order, "short": {1}} {
		if _, err := PublicKeyOf(key); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
