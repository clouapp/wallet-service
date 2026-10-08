package mpc

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/json"
	"math/big"
	"strings"
	"sync"
	"testing"

	"github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
	"github.com/btcsuite/btcd/btcec/v2"
	btcecdsa "github.com/btcsuite/btcd/btcec/v2/ecdsa"

	"github.com/macrowallets/waas/app/services/hdkey"
)

const kddTestChildIndex = 25

var kddKeygenOnce = sync.OnceValues(func() (*KeygenResult, error) {
	return NewTSSService().Keygen(context.Background(), CurveSecp256k1)
})

func kddKeys(t *testing.T) *KeygenResult {
	t.Helper()
	keys, err := kddKeygenOnce()
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func kddChild(t *testing.T, keys *KeygenResult) *hdkey.Secp256k1Child {
	t.Helper()
	child, err := hdkey.DeriveSecp256k1Child(keys.CombinedPubKey, keys.ChainCode, kddTestChildIndex)
	if err != nil {
		t.Fatal(err)
	}
	return child
}

const compactSignatureSize = 64

// verifyDER checks a ceremony signature, which is 64-byte R||S (or DER when R or S
// came back without the compact form).
func verifyDER(t *testing.T, signature, digest, publicKey []byte) bool {
	t.Helper()
	key, err := btcec.ParsePubKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(signature) == compactSignatureSize {
		r := new(big.Int).SetBytes(signature[:32])
		s := new(big.Int).SetBytes(signature[32:])
		return ecdsa.Verify(key.ToECDSA(), digest, r, s)
	}
	parsed, err := btcecdsa.ParseDERSignature(signature)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Verify(digest, key)
}

func TestSign_With_KeyDerivationDeltaSignsForTheChildKey(t *testing.T) {
	keys := kddKeys(t)
	child := kddChild(t, keys)
	digest := sha256.Sum256([]byte("child sweep"))

	der, err := NewTSSService().Sign(context.Background(), CurveSecp256k1, keys.ShareA, keys.ShareB, SignInputs{
		TxHashes:           [][]byte{digest[:]},
		KeyDerivationDelta: child.Tweak,
		ExpectedPublicKey:  child.PublicKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !verifyDER(t, der, digest[:], child.PublicKey) {
		t.Fatal("signature does not verify against the child public key")
	}
	if verifyDER(t, der, digest[:], keys.CombinedPubKey) {
		t.Fatal("a child signature must not verify against the wallet key")
	}
}

func TestSign_Without_DeltaStillSignsForTheWalletKey(t *testing.T) {
	keys := kddKeys(t)
	digest := sha256.Sum256([]byte("base withdrawal"))

	der, err := NewTSSService().Sign(context.Background(), CurveSecp256k1, keys.ShareA, keys.ShareB, SignInputs{
		TxHashes:          [][]byte{digest[:]},
		ExpectedPublicKey: keys.CombinedPubKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !verifyDER(t, der, digest[:], keys.CombinedPubKey) {
		t.Fatal("signature does not verify against the wallet public key")
	}
}

func TestSign_Does_NotMutateTheCallersShares(t *testing.T) {
	keys := kddKeys(t)
	child := kddChild(t, keys)
	shareA := append([]byte(nil), keys.ShareA...)
	shareB := append([]byte(nil), keys.ShareB...)
	digest := sha256.Sum256([]byte("no mutation"))

	if _, err := NewTSSService().Sign(context.Background(), CurveSecp256k1, shareA, shareB, SignInputs{
		TxHashes: [][]byte{digest[:]}, KeyDerivationDelta: child.Tweak, ExpectedPublicKey: child.PublicKey,
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(shareA, keys.ShareA) || !bytes.Equal(shareB, keys.ShareB) {
		t.Fatal("signing must not change the stored shares")
	}
}

func unmarshalSaves(t *testing.T, keys *KeygenResult) (keygen.LocalPartySaveData, keygen.LocalPartySaveData) {
	t.Helper()
	var saveA, saveB keygen.LocalPartySaveData
	if err := json.Unmarshal(keys.ShareA, &saveA); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(keys.ShareB, &saveB); err != nil {
		t.Fatal(err)
	}
	return saveA, saveB
}

func TestApply_KeyDerivationDelta_RejectsMismatchedInputs(t *testing.T) {
	keys := kddKeys(t)
	child := kddChild(t, keys)
	otherChild, err := hdkey.DeriveSecp256k1Child(keys.CombinedPubKey, keys.ChainCode, kddTestChildIndex+1)
	if err != nil {
		t.Fatal(err)
	}
	n := btcec.S256().N
	cases := []struct {
		name     string
		delta    *big.Int
		expected []byte
		want     string
	}{
		{"delta for another child", otherChild.Tweak, child.PublicKey, "does not produce the expected child"},
		{"delta without expected key", child.Tweak, nil, "needs the expected child public key"},
		{"zero delta", big.NewInt(0), child.PublicKey, "out of range"},
		{"delta equal to the order", new(big.Int).Set(n), child.PublicKey, "out of range"},
		{"no delta but child key expected", nil, child.PublicKey, "do not belong to the expected public key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			saveA, saveB := unmarshalSaves(t, keys)
			_, err := applyKeyDerivationDelta(&saveA, &saveB, tc.delta, tc.expected)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestApply_KeyDerivationDelta_RejectsSharesOfDifferentWallets(t *testing.T) {
	keys := kddKeys(t)
	other, err := NewTSSService().Keygen(context.Background(), CurveSecp256k1)
	if err != nil {
		t.Fatal(err)
	}
	saveA, _ := unmarshalSaves(t, keys)
	_, otherB := unmarshalSaves(t, other)
	if _, err := applyKeyDerivationDelta(&saveA, &otherB, nil, nil); err == nil {
		t.Fatal("shares of two wallets must be refused")
	}
}

func TestApply_KeyDerivationDelta_MovesBothPartiesToTheChildKey(t *testing.T) {
	keys := kddKeys(t)
	child := kddChild(t, keys)
	saveA, saveB := unmarshalSaves(t, keys)

	if _, err := applyKeyDerivationDelta(&saveA, &saveB, child.Tweak, child.PublicKey); err != nil {
		t.Fatal(err)
	}
	for name, save := range map[string]keygen.LocalPartySaveData{"A": saveA, "B": saveB} {
		if !bytes.Equal(compressSecp256k1(save.ECDSAPub.X(), save.ECDSAPub.Y()), child.PublicKey) {
			t.Fatalf("party %s still holds the wallet key", name)
		}
	}
}
