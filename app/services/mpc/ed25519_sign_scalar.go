package mpc

import (
	"crypto/ed25519"
	"crypto/sha512"
	"crypto/subtle"
	"fmt"

	"filippo.io/edwards25519"
)

// ed25519ScalarNonceDomain separates the nonce prefix hash from any other use of
// the scalar; changing it changes every future signature, never their validity.
const ed25519ScalarNonceDomain = "macro-wallets/ed25519-scalar-nonce/v1"

// SignEd25519WithScalar signs message with a raw Ed25519 scalar. Genesis keys
// stay raw scalars: there is no seed to expand, so this is not SignEd25519Seed.
// scalarBigEndian is 32 bytes big-endian and must be a canonical non-zero scalar
// whose public point equals publicKey. The caller zeros the scalar. Errors do
// not include it. The signature is verified before it is returned.
func SignEd25519WithScalar(scalarBigEndian, publicKey, message []byte) ([]byte, error) {
	return signEd25519WithScalar(scalarBigEndian, publicKey, message)
}

// signEd25519WithScalar returns an RFC 8032 Ed25519 signature of message for a key
// known only as its scalar a (A = a·B), which is what MPC reconstruction yields; there
// is no seed to expand, so ed25519.NewKeyFromSeed cannot be used. The nonce is
// deterministic like RFC 8032: r = SHA-512(prefix || message) with
// prefix = SHA-512(domain || a)[32:].
func signEd25519WithScalar(scalarBigEndian, publicKey, message []byte) ([]byte, error) {
	if len(scalarBigEndian) != ed25519ScalarSize {
		return nil, fmt.Errorf("ed25519 scalar must be %d bytes, got %d", ed25519ScalarSize, len(scalarBigEndian))
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("ed25519 public key must be %d bytes, got %d", ed25519.PublicKeySize, len(publicKey))
	}

	scalarLittleEndian := make([]byte, ed25519ScalarSize)
	defer zeroBytes(scalarLittleEndian)
	for i := range scalarBigEndian {
		scalarLittleEndian[i] = scalarBigEndian[ed25519ScalarSize-1-i]
	}

	zero := edwards25519.NewScalar()
	secret, err := edwards25519.NewScalar().SetCanonicalBytes(scalarLittleEndian)
	if err != nil {
		return nil, fmt.Errorf("ed25519 scalar is not canonical")
	}
	defer secret.Set(zero)
	if secret.Equal(zero) == 1 {
		return nil, fmt.Errorf("ed25519 scalar is zero")
	}

	derivedPublicKey := new(edwards25519.Point).ScalarBaseMult(secret).Bytes()
	if subtle.ConstantTimeCompare(derivedPublicKey, publicKey) != 1 {
		return nil, fmt.Errorf("ed25519 scalar does not match the signer public key")
	}

	prefixInput := make([]byte, 0, len(ed25519ScalarNonceDomain)+ed25519ScalarSize)
	prefixInput = append(prefixInput, ed25519ScalarNonceDomain...)
	prefixInput = append(prefixInput, scalarLittleEndian...)
	prefixDigest := sha512.Sum512(prefixInput)
	zeroBytes(prefixInput)
	defer zeroBytes(prefixDigest[:])

	nonceInput := make([]byte, 0, sha512.Size/2+len(message))
	nonceInput = append(nonceInput, prefixDigest[sha512.Size/2:]...)
	nonceInput = append(nonceInput, message...)
	nonceDigest := sha512.Sum512(nonceInput)
	zeroBytes(nonceInput)
	defer zeroBytes(nonceDigest[:])

	nonce, err := edwards25519.NewScalar().SetUniformBytes(nonceDigest[:])
	if err != nil {
		return nil, fmt.Errorf("ed25519 nonce: %w", err)
	}
	defer nonce.Set(zero)

	commitment := new(edwards25519.Point).ScalarBaseMult(nonce).Bytes()

	challengeInput := make([]byte, 0, len(commitment)+len(publicKey)+len(message))
	challengeInput = append(challengeInput, commitment...)
	challengeInput = append(challengeInput, publicKey...)
	challengeInput = append(challengeInput, message...)
	challengeDigest := sha512.Sum512(challengeInput)
	challenge, err := edwards25519.NewScalar().SetUniformBytes(challengeDigest[:])
	if err != nil {
		return nil, fmt.Errorf("ed25519 challenge: %w", err)
	}

	response := edwards25519.NewScalar().MultiplyAdd(challenge, secret, nonce)
	defer response.Set(zero)

	signature := make([]byte, 0, ed25519.SignatureSize)
	signature = append(signature, commitment...)
	signature = append(signature, response.Bytes()...)
	if !ed25519.Verify(publicKey, message, signature) {
		return nil, fmt.Errorf("ed25519 scalar signature failed verification")
	}
	return signature, nil
}
