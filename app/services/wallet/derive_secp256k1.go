package wallet

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"

	"github.com/bnb-chain/tss-lib/v2/crypto/ckd"
	"github.com/btcsuite/btcd/btcec/v2"
)

// Secp256k1ChildResult holds the output of a BIP-32 non-hardened child key derivation.
type Secp256k1ChildResult struct {
	ChildPubKey []byte
	ILBytes     []byte
	Index       uint32
}

// deriveSecp256k1Child performs BIP-32 non-hardened child key derivation for secp256k1
// using the tss-lib ckd package. Only non-hardened indices (< 0x80000000) are supported.
func deriveSecp256k1Child(parentPubKey, chainCode []byte, index uint32) (*Secp256k1ChildResult, error) {
	if len(parentPubKey) != 33 {
		return nil, fmt.Errorf("parent pubkey must be 33 bytes, got %d", len(parentPubKey))
	}
	if len(chainCode) != 32 {
		return nil, fmt.Errorf("chain code must be 32 bytes, got %d", len(chainCode))
	}
	if index >= ckd.HardenedKeyStart {
		return nil, fmt.Errorf("hardened indices (>= 0x80000000) are not supported for public child key derivation")
	}

	pub, err := btcec.ParsePubKey(parentPubKey)
	if err != nil {
		return nil, fmt.Errorf("parse parent pubkey: %w", err)
	}

	curve := btcec.S256()

	extKey := &ckd.ExtendedKey{
		PublicKey: ecdsa.PublicKey{
			Curve: curve,
			X:     pub.X(),
			Y:     pub.Y(),
		},
		Depth:      0,
		ChildIndex: 0,
		ChainCode:  chainCode,
		ParentFP:   []byte{0x00, 0x00, 0x00, 0x00},
		Version:    []byte{0x04, 0x88, 0xB2, 0x1E},
	}

	il, childKey, err := ckd.DeriveChildKey(index, extKey, curve)
	if err != nil {
		return nil, fmt.Errorf("derive child key at index %d: %w", index, err)
	}

	childCompressed := compressPoint(childKey.X, childKey.Y)

	ilBytes := make([]byte, 32)
	raw := il.Bytes()
	copy(ilBytes[32-len(raw):], raw)

	return &Secp256k1ChildResult{
		ChildPubKey: childCompressed,
		ILBytes:     ilBytes,
		Index:       index,
	}, nil
}

// compressPoint serializes an elliptic curve point to 33-byte SEC compressed form.
func compressPoint(x, y *big.Int) []byte {
	b := make([]byte, 33)
	if y.Bit(0) == 0 {
		b[0] = 0x02
	} else {
		b[0] = 0x03
	}
	xBytes := x.Bytes()
	copy(b[33-len(xBytes):], xBytes)
	return b
}
