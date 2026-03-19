package mpc

import (
	"bytes"
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
