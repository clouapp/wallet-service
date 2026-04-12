package wallet

import (
	"crypto/ed25519"
	"encoding/hex"
	"testing"
)

func TestDeriveEd25519Child_ProducesValidKey(t *testing.T) {
	masterKey := make([]byte, 32)
	chainCode := make([]byte, 32)
	for i := range masterKey {
		masterKey[i] = byte(i)
	}
	for i := range chainCode {
		chainCode[i] = byte(i + 32)
	}

	result, err := deriveEd25519Child(masterKey, chainCode, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.ChildPrivateKey) != 32 {
		t.Fatalf("expected 32-byte child private key seed, got %d", len(result.ChildPrivateKey))
	}

	privKey := ed25519.NewKeyFromSeed(result.ChildPrivateKey)
	pubKey := privKey.Public().(ed25519.PublicKey)

	if len(pubKey) != 32 {
		t.Fatalf("expected 32-byte public key, got %d", len(pubKey))
	}

	msg := []byte("test message")
	sig := ed25519.Sign(privKey, msg)
	if !ed25519.Verify(pubKey, msg, sig) {
		t.Fatal("derived key should produce valid signatures")
	}
}

func TestDeriveEd25519Child_DifferentIndices(t *testing.T) {
	masterKey := make([]byte, 32)
	chainCode := make([]byte, 32)
	for i := range masterKey {
		masterKey[i] = byte(i)
	}
	for i := range chainCode {
		chainCode[i] = byte(i + 32)
	}

	r0, _ := deriveEd25519Child(masterKey, chainCode, 0)
	r1, _ := deriveEd25519Child(masterKey, chainCode, 1)

	if hex.EncodeToString(r0.ChildPrivateKey) == hex.EncodeToString(r1.ChildPrivateKey) {
		t.Fatal("different indices must produce different child keys")
	}
}

func TestDeriveEd25519Child_Deterministic(t *testing.T) {
	masterKey := make([]byte, 32)
	chainCode := make([]byte, 32)
	for i := range masterKey {
		masterKey[i] = byte(i)
	}
	for i := range chainCode {
		chainCode[i] = byte(i + 32)
	}

	r1, _ := deriveEd25519Child(masterKey, chainCode, 42)
	r2, _ := deriveEd25519Child(masterKey, chainCode, 42)

	if hex.EncodeToString(r1.ChildPrivateKey) != hex.EncodeToString(r2.ChildPrivateKey) {
		t.Fatal("same inputs must produce same child key")
	}
}

func TestDeriveEd25519Child_AddressDerivation(t *testing.T) {
	masterKey := make([]byte, 32)
	chainCode := make([]byte, 32)
	for i := range masterKey {
		masterKey[i] = byte(i)
	}
	for i := range chainCode {
		chainCode[i] = byte(i + 32)
	}

	result, err := deriveEd25519Child(masterKey, chainCode, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	privKey := ed25519.NewKeyFromSeed(result.ChildPrivateKey)
	pubKey := privKey.Public().(ed25519.PublicKey)

	addr, err := deriveSolAddress([]byte(pubKey))
	if err != nil {
		t.Fatalf("deriveSolAddress: %v", err)
	}
	if len(addr) < 32 || len(addr) > 44 {
		t.Fatalf("invalid Solana address length: %d (%s)", len(addr), addr)
	}
}

func TestDeriveEd25519Child_RejectsShortMasterKey(t *testing.T) {
	_, err := deriveEd25519Child(make([]byte, 16), make([]byte, 32), 0)
	if err == nil {
		t.Fatal("expected error for short master key")
	}
}
