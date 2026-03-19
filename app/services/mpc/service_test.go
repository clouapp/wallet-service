package mpc

import (
	"bytes"
	"context"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	passphrase := "correct-horse-battery-staple-123"
	plaintext := []byte("this is a fake mpc key share bytes 32b!")

	encrypted, err := EncryptShare(plaintext, passphrase)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	decrypted, err := DecryptShare(encrypted, passphrase)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Fatal("round-trip mismatch")
	}
}

func TestDecryptWrongPassphrase(t *testing.T) {
	plaintext := []byte("some share data here for testing purposes!")
	encrypted, _ := EncryptShare(plaintext, "correct-passphrase-here")

	_, err := DecryptShare(encrypted, "wrong-passphrase-here!")
	if err == nil {
		t.Fatal("expected error on wrong passphrase, got nil")
	}
}

func TestEncryptedShareFormat(t *testing.T) {
	plaintext := []byte("share data for format test purposes")
	encrypted, err := EncryptShare(plaintext, "test-passphrase-12chars")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// IV must be exactly 12 bytes
	if len(encrypted.IV) != 12 {
		t.Fatalf("IV length: want 12, got %d", len(encrypted.IV))
	}
	// Salt must be exactly 16 bytes
	if len(encrypted.Salt) != 16 {
		t.Fatalf("Salt length: want 16, got %d", len(encrypted.Salt))
	}
	// Ciphertext must be plaintext + 16-byte GCM tag
	if len(encrypted.Ciphertext) != len(plaintext)+16 {
		t.Fatalf("Ciphertext length: want %d, got %d", len(plaintext)+16, len(encrypted.Ciphertext))
	}
}

func TestKeygenSecp256k1ProducesShares(t *testing.T) {
	svc := NewTSSService()

	result, err := svc.Keygen(context.Background(), CurveSecp256k1)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	if len(result.ShareA) == 0 {
		t.Fatal("ShareA is empty")
	}
	if len(result.ShareB) == 0 {
		t.Fatal("ShareB is empty")
	}
	// secp256k1 compressed pubkey = 33 bytes
	if len(result.CombinedPubKey) != 33 {
		t.Fatalf("CombinedPubKey length: want 33, got %d", len(result.CombinedPubKey))
	}
	// Compressed pubkey starts with 0x02 or 0x03
	if result.CombinedPubKey[0] != 0x02 && result.CombinedPubKey[0] != 0x03 {
		t.Fatalf("CombinedPubKey not compressed: first byte %x", result.CombinedPubKey[0])
	}
}

func TestKeygenSharesAreDifferent(t *testing.T) {
	svc := NewTSSService()

	r1, err := svc.Keygen(context.Background(), CurveSecp256k1)
	if err != nil {
		t.Fatalf("keygen 1: %v", err)
	}
	r2, err := svc.Keygen(context.Background(), CurveSecp256k1)
	if err != nil {
		t.Fatalf("keygen 2: %v", err)
	}

	// Two keygens must produce different keys
	if bytes.Equal(r1.ShareA, r2.ShareA) {
		t.Fatal("two keygens produced identical ShareA")
	}
	if bytes.Equal(r1.CombinedPubKey, r2.CombinedPubKey) {
		t.Fatal("two keygens produced identical public keys")
	}
}

func TestSignSecp256k1(t *testing.T) {
	svc := NewTSSService()

	// First keygen to get shares
	result, err := svc.Keygen(context.Background(), CurveSecp256k1)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	// Sign a known 32-byte hash
	msgHash := make([]byte, 32)
	for i := range msgHash {
		msgHash[i] = byte(i)
	}

	sig, err := svc.Sign(
		context.Background(),
		CurveSecp256k1,
		result.ShareA,
		result.ShareB,
		SignInputs{TxHashes: [][]byte{msgHash}},
	)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if len(sig) == 0 {
		t.Fatal("signature is empty")
	}

	// DER-encoded ECDSA signatures are 70-72 bytes typically
	if len(sig) < 64 || len(sig) > 73 {
		t.Fatalf("unexpected signature length: %d", len(sig))
	}
}
