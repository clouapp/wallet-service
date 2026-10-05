package keyexport

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/mr-tron/base58"
)

func TestNewSolanaKeyUsesDeps(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	const fileName = "solana-keygen/index-1.json"

	key, err := NewSolanaKey(SolanaKeyDeps{Seed: seed, KeypairFile: fileName})
	if err != nil {
		t.Fatal(err)
	}
	keypair, err := SolanaKeypair(seed)
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(keypair)

	decoded, err := base58.Decode(key.KeypairBase58)
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(decoded)
	if !bytes.Equal(decoded, keypair) || key.SeedHex != hex.EncodeToString(seed) {
		t.Fatal("seed encoding was not taken from deps")
	}
	if len(key.KeypairJSON) != len(keypair) {
		t.Fatal("json array length was not the keypair length")
	}
	for i, b := range keypair {
		if key.KeypairJSON[i] != int(b) {
			t.Fatal("json array was not the keypair bytes")
		}
	}
	if key.KeypairFile != fileName {
		t.Fatal("keypair file name was not taken from deps")
	}

	other, err := NewSolanaKey(SolanaKeyDeps{Seed: seed, KeypairFile: "solana-keygen/index-2.json"})
	if err != nil {
		t.Fatal(err)
	}
	if other.KeypairBase58 != key.KeypairBase58 || other.SeedHex != key.SeedHex || other.KeypairFile == key.KeypairFile {
		t.Fatal("file name changed the keypair encoding")
	}

	publicKey := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	want := base58.Encode(publicKey)
	fromBase58, err := SolanaAddressOfKeypairBase58(key.KeypairBase58)
	if err != nil || fromBase58 != want {
		t.Fatal("base58 keypair did not re-derive the seed address")
	}
	fromJSON, err := SolanaAddressOfKeypairJSON(key.KeypairJSON)
	if err != nil || fromJSON != want {
		t.Fatal("json keypair did not re-derive the seed address")
	}

	if _, err := NewSolanaKey(SolanaKeyDeps{}); err == nil {
		t.Fatal("a missing seed was accepted")
	}
	if _, err := NewSolanaKey(SolanaKeyDeps{Seed: seed[:len(seed)-1], KeypairFile: fileName}); err == nil {
		t.Fatal("a short seed was accepted")
	}
}
