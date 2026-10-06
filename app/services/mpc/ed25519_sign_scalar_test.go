package mpc

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"testing"

	"filippo.io/edwards25519"
)

// scalarKeyPair returns an ed25519 public key and its scalar in big-endian form,
// the same shape MPC reconstruction yields. It is not a SLIP-0010 seed.
func scalarKeyPair(t *testing.T) (publicKey, scalarBigEndian []byte) {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	digest := sha512.Sum512(seed)
	secret, err := edwards25519.NewScalar().SetBytesWithClamping(digest[:32])
	if err != nil {
		t.Fatal(err)
	}
	little := secret.Bytes()
	scalarBigEndian = make([]byte, len(little))
	for i := range little {
		scalarBigEndian[i] = little[len(little)-1-i]
	}
	publicKey = ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	return publicKey, scalarBigEndian
}

func TestSignEd25519WithScalar_VerifiesAndIsDeterministic(t *testing.T) {
	publicKey, scalar := scalarKeyPair(t)
	message := []byte("solana message bytes")

	first, err := signEd25519WithScalar(scalar, publicKey, message)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(publicKey, message, first) {
		t.Fatal("signature does not verify")
	}
	second, err := signEd25519WithScalar(scalar, publicKey, message)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("same key and message must give the same signature")
	}
	other, err := signEd25519WithScalar(scalar, publicKey, []byte("another message"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first[:32], other[:32]) {
		t.Fatal("different messages must use different nonces")
	}
}

func TestSignEd25519WithScalar_RejectsBadInput(t *testing.T) {
	publicKey, scalar := scalarKeyPair(t)
	otherPublicKey, _ := scalarKeyPair(t)
	nonCanonical := bytes.Repeat([]byte{0xff}, ed25519ScalarSize)
	zero := make([]byte, ed25519ScalarSize)

	cases := []struct {
		name      string
		scalar    []byte
		publicKey []byte
	}{
		{"short scalar", scalar[:31], publicKey},
		{"short public key", scalar, publicKey[:31]},
		{"non-canonical scalar", nonCanonical, publicKey},
		{"zero scalar", zero, publicKey},
		{"scalar for another key", scalar, otherPublicKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := signEd25519WithScalar(tc.scalar, tc.publicKey, []byte("m")); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
