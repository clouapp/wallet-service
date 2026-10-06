package chain

import (
	"crypto/ed25519"
	"crypto/sha512"
	"crypto/subtle"
	"fmt"

	"filippo.io/edwards25519"
)

const (
	ed25519ScalarSize = 32
	// ed25519ScalarNonceDomain separates the nonce prefix hash from any other use of
	// the scalar; changing it changes every future signature, never their validity.
	ed25519ScalarNonceDomain = "macro-wallets/ed25519-scalar-nonce/v1"
)

// signEd25519WithScalar returns an RFC 8032 Ed25519 signature of message for a key
// known only as its scalar a (A = a·B), which is what MPC reconstruction yields; there
// is no seed to expand, so ed25519.NewKeyFromSeed cannot be used. The nonce is
// deterministic like RFC 8032: r = SHA-512(prefix || message) with
// prefix = SHA-512(domain || a)[32:]. scalarBigEndian is 32 bytes big-endian and must
// be a canonical non-zero scalar whose public point equals publicKey. The signature
// is verified before it is returned.
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

// SignEd25519WithScalar signs with a raw scalar. Sweep uses it for Solana genesis
// keys, which stay raw scalars. The adapter only assembles the signed transaction.
func SignEd25519WithScalar(scalarBigEndian, publicKey, message []byte) ([]byte, error) {
	return signEd25519WithScalar(scalarBigEndian, publicKey, message)
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
