package mpc

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"filippo.io/edwards25519"
	eddsaKeygen "github.com/bnb-chain/tss-lib/v2/eddsa/keygen"
	"github.com/bnb-chain/tss-lib/v2/tss"
)

func ed25519PublicFromBigEndianScalar(t *testing.T, scalarBigEndian []byte) []byte {
	t.Helper()
	little := make([]byte, len(scalarBigEndian))
	for i := range scalarBigEndian {
		little[i] = scalarBigEndian[len(scalarBigEndian)-1-i]
	}
	secret, err := edwards25519.NewScalar().SetCanonicalBytes(little)
	if err != nil {
		t.Fatalf("scalar is not canonical: %v", err)
	}
	return new(edwards25519.Point).ScalarBaseMult(secret).Bytes()
}

func TestReconstructEd25519Scalar_MatchesWalletPublicKey(t *testing.T) {
	svc := NewTSSService()
	keys, err := svc.Keygen(context.Background(), CurveEd25519)
	if err != nil {
		t.Fatal(err)
	}

	scalar, err := svc.ReconstructEd25519Scalar(keys.ShareA, keys.ShareB)
	if err != nil {
		t.Fatal(err)
	}
	if len(scalar) != 32 {
		t.Fatalf("scalar length %d", len(scalar))
	}
	if !bytes.Equal(ed25519PublicFromBigEndianScalar(t, scalar), keys.CombinedPubKey) {
		t.Fatal("scalar·B must equal the wallet public key")
	}

	reversed, err := svc.ReconstructEd25519Scalar(keys.ShareB, keys.ShareA)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(scalar, reversed) {
		t.Fatal("share order must not matter")
	}
}

// The legacy share sum seeds SLIP-0010 child derivation; it is not the genesis key.
func TestReconstructEd25519PrivateKey_IsNotTheGenesisKey(t *testing.T) {
	svc := NewTSSService()
	keys, err := svc.Keygen(context.Background(), CurveEd25519)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := svc.ReconstructEd25519PrivateKey(keys.ShareA, keys.ShareB)
	if err != nil {
		t.Fatal(err)
	}
	asSeed := ed25519.NewKeyFromSeed(sum).Public().(ed25519.PublicKey)
	if bytes.Equal(asSeed, keys.CombinedPubKey) {
		t.Fatal("share sum unexpectedly matches the wallet public key")
	}
}

func TestReconstructEd25519Scalar_RejectsMismatchedShares(t *testing.T) {
	svc := NewTSSService()
	first, err := svc.Keygen(context.Background(), CurveEd25519)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Keygen(context.Background(), CurveEd25519)
	if err != nil {
		t.Fatal(err)
	}

	tamperedXi := tamperShare(t, first.ShareB, func(save *eddsaKeygen.LocalPartySaveData) {
		save.Xi = new(big.Int).Add(save.Xi, big.NewInt(1))
	})
	idAliasedModN := tamperShare(t, first.ShareB, func(save *eddsaKeygen.LocalPartySaveData) {
		var other eddsaKeygen.LocalPartySaveData
		if err := json.Unmarshal(first.ShareA, &other); err != nil {
			t.Fatal(err)
		}
		save.ShareID = new(big.Int).Add(other.ShareID, tss.Edwards().Params().N)
	})

	cases := []struct {
		name           string
		shareA, shareB []byte
	}{
		{"shares from different wallets", first.ShareA, second.ShareB},
		{"same party twice", first.ShareA, first.ShareA},
		{"tampered share of the same wallet", first.ShareA, tamperedXi},
		{"share ids equal modulo the curve order", first.ShareA, idAliasedModN},
		{"not json", []byte("garbage"), first.ShareB},
		{"empty json", []byte("{}"), first.ShareB},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.ReconstructEd25519Scalar(tc.shareA, tc.shareB); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestReconstructEd25519Scalar_MalformedShareErrorHidesInput(t *testing.T) {
	secretLooking := []byte(`{"Xi":"not-a-number-but-looks-secret"}`)
	_, err := NewTSSService().ReconstructEd25519Scalar(secretLooking, secretLooking)
	if err == nil || strings.Contains(err.Error(), "looks-secret") {
		t.Fatalf("error must not echo share content: %v", err)
	}
}

func TestInterpolateTwoSharesAtZero_RejectsOutOfRangeInputs(t *testing.T) {
	n := tss.Edwards().Params().N
	one, two := big.NewInt(1), big.NewInt(2)
	cases := map[string][4]*big.Int{
		"zero id":              {big.NewInt(0), one, two, one},
		"id equal to order":    {new(big.Int).Set(n), one, two, one},
		"ids equal mod order":  {two, one, new(big.Int).Add(n, two), one},
		"negative share":       {one, big.NewInt(-1), two, one},
		"share equal to order": {one, new(big.Int).Set(n), two, one},
		"same id":              {two, one, two, one},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := interpolateTwoSharesAtZero(in[0], in[1], in[2], in[3], n); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestInterpolateTwoSharesAtZero_RecoversLineIntercept(t *testing.T) {
	n := tss.Edwards().Params().N
	// f(x) = 7 + 3x → f(1) = 10, f(5) = 22; ids above n behave like their residue.
	for _, idB := range []*big.Int{big.NewInt(5), new(big.Int).Add(n, big.NewInt(5))} {
		secret, err := interpolateTwoSharesAtZero(big.NewInt(1), big.NewInt(10), idB, big.NewInt(22), n)
		if err != nil {
			t.Fatal(err)
		}
		if secret.Int64() != 7 {
			t.Fatalf("f(0) = %s, want 7", secret)
		}
	}
}

func TestDecryptShare_RejectsWrongIVLength(t *testing.T) {
	enc, err := EncryptShare([]byte("seed"), "passphrase-long-enough")
	if err != nil {
		t.Fatal(err)
	}
	enc.IV = enc.IV[:8]
	if _, err := DecryptShare(enc, "passphrase-long-enough"); err == nil {
		t.Fatal("expected an error instead of a panic")
	}
	if _, err := DecryptShare(nil, "passphrase-long-enough"); err == nil {
		t.Fatal("expected an error for a nil share")
	}
}

func tamperShare(t *testing.T, share []byte, mutate func(*eddsaKeygen.LocalPartySaveData)) []byte {
	t.Helper()
	var save eddsaKeygen.LocalPartySaveData
	if err := json.Unmarshal(share, &save); err != nil {
		t.Fatal(err)
	}
	mutate(&save)
	out, err := json.Marshal(&save)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
