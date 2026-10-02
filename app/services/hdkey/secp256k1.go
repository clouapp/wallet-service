// Package hdkey holds the BIP-32 non-hardened derivation shared by address generation
// and signing, so a child address and the key that signs for it always come from the
// same math.
package hdkey

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"

	"github.com/bnb-chain/tss-lib/v2/crypto/ckd"
	"github.com/btcsuite/btcd/btcec/v2"
)

const (
	CompressedPublicKeySize = 33
	ChainCodeSize           = 32
	PrivateKeySize          = 32
)

// bip32PublicVersion is the xpub version tss-lib's ckd expects on an extended key; it
// does not enter the child math.
var bip32PublicVersion = []byte{0x04, 0x88, 0xB2, 0x1E}

// Secp256k1Child is the BIP-32 non-hardened child at Index of an MPC wallet key:
// child public key = parent public key + Tweak·G, and child private key =
// parent private key + Tweak (mod n). Tweak is BIP-32's IL.
type Secp256k1Child struct {
	PublicKey []byte
	Tweak     *big.Int
	Index     uint32
}

// TweakBytes is Tweak as 32 big-endian bytes.
func (c *Secp256k1Child) TweakBytes() []byte {
	out := make([]byte, PrivateKeySize)
	c.Tweak.FillBytes(out)
	return out
}

// DeriveSecp256k1Child derives the non-hardened child at index from the wallet's
// compressed public key and chain code. Hardened indices need the parent private key
// and are refused.
func DeriveSecp256k1Child(parentPublicKey, chainCode []byte, index uint32) (*Secp256k1Child, error) {
	if len(parentPublicKey) != CompressedPublicKeySize {
		return nil, fmt.Errorf("parent pubkey must be %d bytes, got %d", CompressedPublicKeySize, len(parentPublicKey))
	}
	if len(chainCode) != ChainCodeSize {
		return nil, fmt.Errorf("chain code must be %d bytes, got %d", ChainCodeSize, len(chainCode))
	}
	if index >= ckd.HardenedKeyStart {
		return nil, fmt.Errorf("hardened indices (>= 0x80000000) are not supported for public child key derivation")
	}
	parent, err := btcec.ParsePubKey(parentPublicKey)
	if err != nil {
		return nil, fmt.Errorf("parse parent pubkey: %w", err)
	}
	extended := &ckd.ExtendedKey{
		PublicKey:  ecdsa.PublicKey{Curve: btcec.S256(), X: parent.X(), Y: parent.Y()},
		Depth:      0,
		ChildIndex: 0,
		ChainCode:  chainCode,
		ParentFP:   []byte{0x00, 0x00, 0x00, 0x00},
		Version:    bip32PublicVersion,
	}
	tweak, child, err := ckd.DeriveChildKey(index, extended, btcec.S256())
	if err != nil {
		return nil, fmt.Errorf("derive child key at index %d: %w", index, err)
	}
	return &Secp256k1Child{
		PublicKey: CompressPoint(child.X, child.Y),
		Tweak:     tweak,
		Index:     index,
	}, nil
}

// ChildPrivateKey returns (parent private key + tweak) mod n as 32 big-endian bytes.
// The caller must zero both the input and the result.
func ChildPrivateKey(parentPrivateKey []byte, tweak *big.Int) ([]byte, error) {
	if len(parentPrivateKey) != PrivateKeySize {
		return nil, fmt.Errorf("parent private key must be %d bytes", PrivateKeySize)
	}
	n := btcec.S256().N
	if tweak == nil || tweak.Sign() <= 0 || tweak.Cmp(n) >= 0 {
		return nil, fmt.Errorf("child tweak is out of range")
	}
	parent := new(big.Int).SetBytes(parentPrivateKey)
	defer wipe(parent)
	if parent.Sign() == 0 || parent.Cmp(n) >= 0 {
		return nil, fmt.Errorf("parent private key is out of range")
	}
	sum := new(big.Int).Add(parent, tweak)
	defer wipe(sum)
	child := new(big.Int).Mod(sum, n)
	defer wipe(child)
	if child.Sign() == 0 {
		return nil, fmt.Errorf("child private key is zero")
	}
	out := make([]byte, PrivateKeySize)
	child.FillBytes(out)
	return out, nil
}

// PublicKeyOf returns the compressed public key of a 32-byte private key.
func PublicKeyOf(privateKey []byte) ([]byte, error) {
	if len(privateKey) != PrivateKeySize {
		return nil, fmt.Errorf("private key must be %d bytes", PrivateKeySize)
	}
	scalar := new(big.Int).SetBytes(privateKey)
	defer wipe(scalar)
	if scalar.Sign() == 0 || scalar.Cmp(btcec.S256().N) >= 0 {
		return nil, fmt.Errorf("private key is out of range")
	}
	key, _ := btcec.PrivKeyFromBytes(privateKey)
	defer key.Zero()
	return key.PubKey().SerializeCompressed(), nil
}

// CompressPoint serializes a curve point to 33-byte SEC compressed form.
func CompressPoint(x, y *big.Int) []byte {
	out := make([]byte, CompressedPublicKeySize)
	if y.Bit(0) == 0 {
		out[0] = 0x02
	} else {
		out[0] = 0x03
	}
	x.FillBytes(out[1:])
	return out
}

// wipe overwrites the words backing n; SetInt64(0) alone leaves the old limbs behind.
func wipe(n *big.Int) {
	words := n.Bits()
	words = words[:cap(words)]
	for i := range words {
		words[i] = 0
	}
	n.SetInt64(0)
}
