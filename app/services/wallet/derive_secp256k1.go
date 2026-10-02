package wallet

import (
	"math/big"

	"github.com/macrowallets/waas/app/services/hdkey"
)

// Secp256k1ChildResult holds the output of a BIP-32 non-hardened child key derivation.
type Secp256k1ChildResult struct {
	ChildPubKey []byte
	ILBytes     []byte
	Index       uint32
}

// deriveSecp256k1Child performs BIP-32 non-hardened child key derivation for secp256k1.
// Signing for the child re-derives the same tweak through hdkey, so both stay in step.
func deriveSecp256k1Child(parentPubKey, chainCode []byte, index uint32) (*Secp256k1ChildResult, error) {
	child, err := hdkey.DeriveSecp256k1Child(parentPubKey, chainCode, index)
	if err != nil {
		return nil, err
	}
	return &Secp256k1ChildResult{
		ChildPubKey: child.PublicKey,
		ILBytes:     child.TweakBytes(),
		Index:       index,
	}, nil
}

// compressPoint serializes an elliptic curve point to 33-byte SEC compressed form.
func compressPoint(x, y *big.Int) []byte {
	return hdkey.CompressPoint(x, y)
}
