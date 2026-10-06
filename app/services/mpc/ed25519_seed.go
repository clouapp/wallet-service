package mpc

import (
	"crypto/ed25519"
	"fmt"
)

// SignEd25519Seed signs message with a 32-byte SLIP-0010 seed, the importable
// form of a Solana child key. The signature is the standard 64-byte Ed25519
// signature and publicKey is the 32-byte verification key. Genesis keys stay
// raw scalars and are signed with chain.SignEd25519WithScalar. The caller zeros
// seed. Errors do not include it.
func SignEd25519Seed(seed, message []byte) (signature, publicKey []byte, err error) {
	if len(seed) != ed25519.SeedSize {
		return nil, nil, fmt.Errorf("ed25519 seed must be %d bytes", ed25519.SeedSize)
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	defer zeroBytes(privateKey)
	publicKey = append([]byte(nil), privateKey.Public().(ed25519.PublicKey)...)
	return ed25519.Sign(privateKey, message), publicKey, nil
}
