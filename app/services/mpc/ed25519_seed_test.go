package mpc

import (
	"bytes"
	"crypto/ed25519"
	"testing"
)

func TestSign_Ed25519Seed_MatchesTheImportableSeed(t *testing.T) {
	seed := bytes.Repeat([]byte{0x07}, ed25519.SeedSize)
	message := []byte("solana-message")
	signature, publicKey, err := SignEd25519Seed(seed, message)
	if err != nil {
		t.Fatal(err)
	}
	wantPublic := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	if !bytes.Equal(publicKey, wantPublic) {
		t.Fatal("public key is not the seed's verification key")
	}
	if !ed25519.Verify(publicKey, message, signature) {
		t.Fatal("signature does not verify")
	}
	want := ed25519.Sign(ed25519.NewKeyFromSeed(seed), message)
	if !bytes.Equal(signature, want) {
		t.Fatal("signature differs from the standard library seed signature")
	}
}

func TestSign_Ed25519Seed_RejectsAShortSeed(t *testing.T) {
	if _, _, err := SignEd25519Seed(bytes.Repeat([]byte{1}, ed25519.SeedSize-1), []byte("m")); err == nil {
		t.Fatal("expected a short seed to be rejected")
	}
	if _, _, err := SignEd25519Seed(nil, []byte("m")); err == nil {
		t.Fatal("expected an empty seed to be rejected")
	}
}
