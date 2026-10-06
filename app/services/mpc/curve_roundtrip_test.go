package mpc

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"testing"
)

const roundTripPassphrase = "correct-horse-battery-staple"

// mockSecretStore is the SecretStore port in memory. It never calls AWS.
type mockSecretStore struct {
	values map[string][]byte
}

func (s *mockSecretStore) Create(_ context.Context, name string, secretBinary []byte) (string, error) {
	if name == "" || len(secretBinary) == 0 {
		return "", fmt.Errorf("secret store: missing name or value")
	}
	if s.values == nil {
		s.values = map[string][]byte{}
	}
	arn := "memory:" + name
	if _, exists := s.values[arn]; exists {
		return "", fmt.Errorf("secret store: already exists")
	}
	s.values[arn] = append([]byte(nil), secretBinary...)
	return arn, nil
}

func (s *mockSecretStore) Binary(_ context.Context, secretID string) ([]byte, error) {
	raw, ok := s.values[secretID]
	if !ok {
		return nil, fmt.Errorf("secret store: not found")
	}
	return append([]byte(nil), raw...), nil
}

// roundTripShares encrypts share A and stores share B, then returns both the
// way a later signing call would load them. The store is not AWS.
func roundTripShares(t *testing.T, name string, shareA, shareB []byte) (recoveredA, recoveredB []byte) {
	t.Helper()
	encrypted, err := EncryptShare(shareA, roundTripPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	recoveredA, err = DecryptShare(encrypted, roundTripPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(recoveredA, shareA) {
		t.Fatal("share A did not round-trip")
	}

	store := &mockSecretStore{}
	arn, err := store.Create(context.Background(), name, shareB)
	if err != nil {
		t.Fatal(err)
	}
	recoveredB, err = store.Binary(context.Background(), arn)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(recoveredB, shareB) || len(recoveredB) == 0 || &recoveredB[0] == &shareB[0] {
		t.Fatal("share B did not round-trip")
	}
	return recoveredA, recoveredB
}

func TestKeygenSignRoundTripSecp256k1(t *testing.T) {
	svc := NewTSSService()
	keys, err := svc.Keygen(context.Background(), CurveSecp256k1)
	if err != nil {
		t.Fatal(err)
	}
	shareA, shareB := roundTripShares(t, "vault/wallet/secp256k1/share-b", keys.ShareA, keys.ShareB)
	defer zeroBytes(shareA)
	defer zeroBytes(shareB)
	zeroBytes(keys.ShareA)
	zeroBytes(keys.ShareB)

	digest := sha256.Sum256([]byte("secp256k1 round trip"))
	signature, err := svc.Sign(context.Background(), CurveSecp256k1, shareA, shareB, SignInputs{
		TxHashes:          [][]byte{digest[:]},
		ExpectedPublicKey: keys.CombinedPubKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !verifyDER(t, signature, digest[:], keys.CombinedPubKey) {
		t.Fatal("signature does not verify against the wallet public key")
	}
}

func TestKeygenSignRoundTripEd25519(t *testing.T) {
	svc := NewTSSService()
	keys, err := svc.Keygen(context.Background(), CurveEd25519)
	if err != nil {
		t.Fatal(err)
	}
	shareA, shareB := roundTripShares(t, "vault/wallet/ed25519/share-b", keys.ShareA, keys.ShareB)
	defer zeroBytes(shareA)
	defer zeroBytes(shareB)
	zeroBytes(keys.ShareA)
	zeroBytes(keys.ShareB)

	// Genesis is a raw scalar. The SLIP-0010 master is a 32-byte seed input
	// for children and must not verify as this public key.
	scalar, err := svc.ReconstructEd25519Scalar(shareA, shareB)
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(scalar)
	message := []byte("ed25519 round trip")
	signature, err := SignEd25519WithScalar(scalar, keys.CombinedPubKey, message)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(keys.CombinedPubKey, message, signature) {
		t.Fatal("signature does not verify against the wallet public key")
	}

	master, err := svc.ReconstructEd25519PrivateKey(shareA, shareB)
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(master)
	if len(master) != ed25519.SeedSize {
		t.Fatalf("child master must be a %d-byte seed, got %d", ed25519.SeedSize, len(master))
	}
	seedSignature, seedPublic, err := SignEd25519Seed(master, message)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(seedPublic, keys.CombinedPubKey) || ed25519.Verify(keys.CombinedPubKey, message, seedSignature) {
		t.Fatal("genesis key must stay a raw scalar")
	}
}
